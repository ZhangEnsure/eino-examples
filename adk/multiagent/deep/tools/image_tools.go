package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ============================================================
// 图片 LLM 客户端（直接 HTTP 调用，不依赖 eino-ext openai 适配层）
// 参考 ImageInfra 实现，直接解析原始 JSON 响应以提取图片数据
// ============================================================

// picClient 图片处理专用的 HTTP 客户端
type picClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// newPicClient 创建图片处理专用客户端
func newPicClient() (*picClient, error) {
	modelName := os.Getenv("OPENAI_PIC_MODEL")
	if modelName == "" {
		return nil, fmt.Errorf("未设置 OPENAI_PIC_MODEL 环境变量")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		return nil, fmt.Errorf("未设置 OPENAI_BASE_URL 环境变量")
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("未设置 OPENAI_API_KEY 环境变量")
	}

	return &picClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   modelName,
		client:  &http.Client{Timeout: 300 * time.Second},
	}, nil
}

// chatRequest 请求结构
type picChatRequest struct {
	Model    string           `json:"model"`
	Messages []picChatMessage `json:"messages"`
}

// chatMessage 消息结构，Content 支持字符串或多模态数组
type picChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// doRequest 发送请求到 LLM API，返回原始响应体
func (c *picClient) doRequest(ctx context.Context, reqBody picChatRequest) ([]byte, error) {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	url := strings.TrimRight(c.baseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回错误状态码 %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// extractImage 从原始 JSON 响应中提取图片数据
// 支持多种格式：inline_data、image_url、venus_multimodal_url、data URI、raw base64
func (c *picClient) extractImage(respBody []byte) ([]byte, string, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, "", fmt.Errorf("解析响应 JSON 失败: %w", err)
	}
	if errObj, ok := raw["error"]; ok && errObj != nil {
		return nil, "", fmt.Errorf("API 返回错误: %v", errObj)
	}

	choices, ok := raw["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil, "", fmt.Errorf("响应中没有 choices")
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil, "", fmt.Errorf("choices[0] 格式异常")
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return nil, "", fmt.Errorf("message 格式异常")
	}
	content := message["content"]

	// 情况1: content 是数组（多模态响应）
	if contentArr, ok := content.([]interface{}); ok {
		for _, part := range contentArr {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			// venus_multimodal_url 格式
			if venusURL, ok := partMap["venus_multimodal_url"].(map[string]interface{}); ok {
				if u, ok := venusURL["url"].(string); ok {
					return c.handleImageURL(u)
				}
			}
			if partMap["type"] == "venus_multimodal_url" {
				if venusURL, ok := partMap["venus_multimodal_url"].(map[string]interface{}); ok {
					if u, ok := venusURL["url"].(string); ok {
						return c.handleImageURL(u)
					}
				}
			}
			// Gemini inline_data 格式
			if inlineData, ok := partMap["inline_data"].(map[string]interface{}); ok {
				if data, ok := inlineData["data"].(string); ok {
					mimeType := "image/png"
					if mt, ok := inlineData["mime_type"].(string); ok && mt != "" {
						mimeType = mt
					}
					decoded, err := base64.StdEncoding.DecodeString(data)
					if err != nil {
						return nil, "", fmt.Errorf("inline_data base64 解码失败: %w", err)
					}
					ext := mimeToExt(mimeType)
					fmt.Printf("[extractImage] 从 inline_data 提取图片成功, mimeType=%s, size=%d bytes\n", mimeType, len(decoded))
					return decoded, ext, nil
				}
			}
			// OpenAI image_url 格式
			if imgURL, ok := partMap["image_url"].(map[string]interface{}); ok {
				if u, ok := imgURL["url"].(string); ok {
					return c.handleImageURL(u)
				}
			}
			if partMap["type"] == "image_url" {
				if imgURL, ok := partMap["image_url"].(map[string]interface{}); ok {
					if u, ok := imgURL["url"].(string); ok {
						return c.handleImageURL(u)
					}
				}
			}
		}
		return nil, "", fmt.Errorf("content 数组中未找到图片数据")
	}

	// 情况2: content 是字符串
	if contentStr, ok := content.(string); ok {
		// 尝试直接 base64 解码
		if decoded, err := base64.StdEncoding.DecodeString(contentStr); err == nil && len(decoded) > 100 {
			if isPNG(decoded) || isJPEG(decoded) {
				mimeType := http.DetectContentType(decoded)
				ext := mimeToExt(mimeType)
				fmt.Printf("[extractImage] 从 content 字符串 (raw base64) 提取图片成功, size=%d bytes\n", len(decoded))
				return decoded, ext, nil
			}
		}
		// 尝试 data URI 格式
		if strings.Contains(contentStr, "data:image") {
			data, ext, err := extractDataURI(contentStr)
			if err == nil {
				fmt.Printf("[extractImage] 从 content 字符串 (data URI) 提取图片成功, size=%d bytes\n", len(data))
				return data, ext, nil
			}
		}
		return nil, "", fmt.Errorf("模型返回了文本而非图片: %s", truncateString(contentStr, 200))
	}

	return nil, "", fmt.Errorf("无法解析 content 类型: %T", content)
}

// extractText 从原始 JSON 响应中提取文本内容（用于图片分析）
func (c *picClient) extractText(respBody []byte) (string, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return "", fmt.Errorf("解析响应 JSON 失败: %w", err)
	}
	if errObj, ok := raw["error"]; ok && errObj != nil {
		return "", fmt.Errorf("API 返回错误: %v", errObj)
	}

	choices, ok := raw["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", fmt.Errorf("响应中没有 choices")
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("choices[0] 格式异常")
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("message 格式异常")
	}
	content := message["content"]

	// content 是字符串
	if contentStr, ok := content.(string); ok {
		return contentStr, nil
	}

	// content 是数组，提取 text 部分
	if contentArr, ok := content.([]interface{}); ok {
		var texts []string
		for _, part := range contentArr {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			if partMap["type"] == "text" {
				if text, ok := partMap["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n"), nil
		}
	}

	return "", fmt.Errorf("无法从响应中提取文本内容")
}

// handleImageURL 处理图片 URL（支持 data URI 和 HTTP URL）
func (c *picClient) handleImageURL(url string) ([]byte, string, error) {
	if strings.HasPrefix(url, "data:image") {
		return extractDataURI(url)
	}
	if strings.HasPrefix(url, "http") {
		imgData, mimeType, err := downloadImage(url)
		if err != nil {
			return nil, "", fmt.Errorf("下载图片失败: %w", err)
		}
		ext := mimeToExt(mimeType)
		return imgData, ext, nil
	}
	return nil, "", fmt.Errorf("不支持的图片 URL 格式")
}

// ============================================================
// 工具1: image_generate - 从文本描述生成图像（返回 CDN URL）
// ============================================================

var imageGenerateToolInfo = &schema.ToolInfo{
	Name: "image_generate",
	Desc: `从文本描述生成图像，返回 CDN URL。
* 输入一段文字描述，AI 将根据描述生成对应的图片。
* 返回的是上传到 COS 的 CDN 链接，可以直接在浏览器中查看。
* 支持中文和英文描述。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"prompt": {
			Type:     schema.String,
			Desc:     "图片描述文本，例如：'一只可爱的橘猫在阳光下打盹'、'A futuristic city skyline at sunset'",
			Required: true,
		},
		"size": {
			Type:     schema.String,
			Desc:     "图片尺寸，可选值：'1024x1024'（默认）、'512x512'、'1792x1024'、'1024x1792'",
			Required: false,
		},
	}),
}

// NewImageGenerateTool 创建图片生成工具
func NewImageGenerateTool(cosManager *ImageCOSManager) tool.InvokableTool {
	return &imageGenerateTool{cosManager: cosManager}
}

type imageGenerateTool struct {
	cosManager *ImageCOSManager
}

func (t *imageGenerateTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return imageGenerateToolInfo, nil
}

type imageGenerateInput struct {
	Prompt string `json:"prompt"`
	Size   string `json:"size,omitempty"`
}

func (t *imageGenerateTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	input := &imageGenerateInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}
	if input.Prompt == "" {
		return "prompt 参数不能为空", nil
	}

	// 创建图片处理客户端
	client, err := newPicClient()
	if err != nil {
		return "", fmt.Errorf("创建图片客户端失败: %w", err)
	}

	// 构建请求
	reqBody := picChatRequest{
		Model: client.model,
		Messages: []picChatMessage{
			{Role: "system", Content: "你是一个专业的图片生成助手。请根据用户的描述生成一张高质量的图片。直接生成图片，不要输出任何文字说明。"},
			{Role: "user", Content: fmt.Sprintf("请根据以下描述生成一张图片：%s", input.Prompt)},
		},
	}

	fmt.Printf("[image_generate] 开始生成图片, model: %s, prompt: %s\n", client.model, truncateString(input.Prompt, 100))
	startTime := time.Now()

	// 发送请求
	respBody, err := client.doRequest(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("图片生成失败: %w", err)
	}
	fmt.Printf("[image_generate] API 调用完成, 耗时: %v, 响应大小: %d bytes\n", time.Since(startTime), len(respBody))

	// 从响应中提取图片数据
	imageData, ext, err := client.extractImage(respBody)
	if err != nil {
		return "", fmt.Errorf("提取图片数据失败: %w", err)
	}

	// 上传到 COS
	cdnURL, err := t.cosManager.Upload(imageData, ext)
	if err != nil {
		return "", fmt.Errorf("上传图片到 COS 失败: %w", err)
	}

	result := map[string]string{
		"status":  "success",
		"url":     cdnURL,
		"prompt":  input.Prompt,
		"message": "图片已成功生成并上传",
	}
	resultJSON, _ := json.Marshal(result)
	return string(resultJSON), nil
}

// ============================================================
// 工具2: image_edit - 编辑图像（背景去除、对象替换、颜色调整等）
// ============================================================

var imageEditToolInfo = &schema.ToolInfo{
	Name: "image_edit",
	Desc: `编辑图像，支持背景去除、对象替换、颜色调整、风格转换等操作。
* 输入一张图片的 URL 或本地文件路径和编辑指令，AI 将根据指令对图片进行编辑。
* 返回编辑后图片的 CDN URL。
* 支持网络图片 URL（http/https 开头）和本地文件路径。
* 支持的操作包括但不限于：背景去除、背景替换、对象替换、颜色调整、风格转换、添加文字、裁剪等。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"image_url": {
			Type:     schema.String,
			Desc:     "待编辑图片的来源，可以是网络 URL（http/https 开头）或本地文件路径",
			Required: true,
		},
		"instruction": {
			Type:     schema.String,
			Desc:     "编辑指令，例如：'去除背景'、'将背景替换为海滩'、'调整为暖色调'、'转换为油画风格'",
			Required: true,
		},
	}),
}

// NewImageEditTool 创建图片编辑工具
func NewImageEditTool(cosManager *ImageCOSManager) tool.InvokableTool {
	return &imageEditTool{cosManager: cosManager}
}

type imageEditTool struct {
	cosManager *ImageCOSManager
}

func (t *imageEditTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return imageEditToolInfo, nil
}

type imageEditInput struct {
	ImageURL    string `json:"image_url"`
	Instruction string `json:"instruction"`
}

func (t *imageEditTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	input := &imageEditInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}
	if input.ImageURL == "" {
		return "image_url 参数不能为空", nil
	}
	if input.Instruction == "" {
		return "instruction 参数不能为空", nil
	}

	// 加载原始图片（支持本地文件路径和网络 URL）
	var imageData []byte
	var mimeType string
	var err error
	if strings.HasPrefix(input.ImageURL, "http://") || strings.HasPrefix(input.ImageURL, "https://") {
		// 网络图片：下载
		imageData, mimeType, err = downloadImage(input.ImageURL)
		if err != nil {
			return "", fmt.Errorf("下载图片失败: %w", err)
		}
	} else {
		// 本地文件：直接读取
		imageData, err = os.ReadFile(input.ImageURL)
		if err != nil {
			return "", fmt.Errorf("读取本地图片失败: %w, 路径: %s", err, input.ImageURL)
		}
		mimeType = http.DetectContentType(imageData)
	}

	// 创建图片处理客户端
	client, err := newPicClient()
	if err != nil {
		return "", fmt.Errorf("创建图片客户端失败: %w", err)
	}

	// 将图片编码为 base64
	b64 := base64.StdEncoding.EncodeToString(imageData)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)

	// 构建多模态编辑请求
	reqBody := picChatRequest{
		Model: client.model,
		Messages: []picChatMessage{
			{Role: "system", Content: "你是一个专业的图片编辑助手。请根据用户的指令对提供的图片进行编辑，直接输出编辑后的图片，不要输出任何文字说明。"},
			{Role: "user", Content: []map[string]any{
				{
					"type": "image_url",
					"image_url": map[string]string{
						"url": dataURL,
					},
				},
				{
					"type": "text",
					"text": fmt.Sprintf("请对这张图片执行以下编辑操作：%s", input.Instruction),
				},
			}},
		},
	}

	fmt.Printf("[image_edit] 开始编辑图片, model: %s, instruction: %s\n", client.model, truncateString(input.Instruction, 100))
	startTime := time.Now()

	// 发送请求
	respBody, err := client.doRequest(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("图片编辑失败: %w", err)
	}
	fmt.Printf("[image_edit] API 调用完成, 耗时: %v, 响应大小: %d bytes\n", time.Since(startTime), len(respBody))

	// 从响应中提取编辑后的图片
	editedImageData, ext, err := client.extractImage(respBody)
	if err != nil {
		return "", fmt.Errorf("提取编辑后图片失败: %w", err)
	}

	// 上传到 COS
	cdnURL, err := t.cosManager.Upload(editedImageData, ext)
	if err != nil {
		return "", fmt.Errorf("上传编辑后图片到 COS 失败: %w", err)
	}

	result := map[string]string{
		"status":      "success",
		"url":         cdnURL,
		"instruction": input.Instruction,
		"message":     "图片已成功编辑并上传",
	}
	resultJSON, _ := json.Marshal(result)
	return string(resultJSON), nil
}

// ============================================================
// 工具3: see_image - 查看和分析本地/网络图片
// ============================================================

var seeImageToolInfo = &schema.ToolInfo{
	Name: "see_image",
	Desc: `查看和分析本地或网络图片。
* 输入图片 URL 或本地文件路径，以及分析问题，AI 将对图片进行分析并返回文字描述。
* 支持网络图片 URL（http/https 开头）和本地文件路径。
* 可以用于图片内容描述、OCR 文字识别、物体识别、场景分析等。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"image_source": {
			Type:     schema.String,
			Desc:     "图片来源，可以是网络 URL（http/https 开头）或本地文件路径",
			Required: true,
		},
		"question": {
			Type:     schema.String,
			Desc:     "关于图片的问题或分析指令，例如：'描述这张图片的内容'、'图片中有什么文字'、'分析图片中的物体'",
			Required: true,
		},
	}),
}

// NewSeeImageTool 创建图片查看分析工具
func NewSeeImageTool() tool.InvokableTool {
	return &seeImageTool{}
}

type seeImageTool struct{}

func (t *seeImageTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return seeImageToolInfo, nil
}

type seeImageInput struct {
	ImageSource string `json:"image_source"`
	Question    string `json:"question"`
}

func (t *seeImageTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	input := &seeImageInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}
	if input.ImageSource == "" {
		return "image_source 参数不能为空", nil
	}
	if input.Question == "" {
		return "question 参数不能为空", nil
	}

	// 创建图片处理客户端
	client, err := newPicClient()
	if err != nil {
		return "", fmt.Errorf("创建图片客户端失败: %w", err)
	}

	var dataURL string
	var mimeType string

	if strings.HasPrefix(input.ImageSource, "http://") || strings.HasPrefix(input.ImageSource, "https://") {
		// 网络图片：下载后转为 base64
		imageData, mime, err := downloadImage(input.ImageSource)
		if err != nil {
			return "", fmt.Errorf("下载图片失败: %w", err)
		}
		mimeType = mime
		b64 := base64.StdEncoding.EncodeToString(imageData)
		dataURL = fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	} else {
		// 本地文件
		fileData, err := os.ReadFile(input.ImageSource)
		if err != nil {
			return fmt.Sprintf("读取本地图片失败: %v, 路径: %s", err, input.ImageSource), nil
		}
		mimeType = http.DetectContentType(fileData)
		b64 := base64.StdEncoding.EncodeToString(fileData)
		dataURL = fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	}

	// 构建多模态分析请求
	reqBody := picChatRequest{
		Model: client.model,
		Messages: []picChatMessage{
			{Role: "system", Content: "你是一个专业的图片分析助手。请仔细观察用户提供的图片，并根据用户的问题给出详细、准确的分析结果。"},
			{Role: "user", Content: []map[string]any{
				{
					"type": "image_url",
					"image_url": map[string]string{
						"url": dataURL,
					},
				},
				{
					"type": "text",
					"text": input.Question,
				},
			}},
		},
	}

	fmt.Printf("[see_image] 开始分析图片, model: %s, source: %s\n", client.model, truncateString(input.ImageSource, 100))
	startTime := time.Now()

	// 发送请求
	respBody, err := client.doRequest(ctx, reqBody)
	if err != nil {
		return "", fmt.Errorf("图片分析失败: %w", err)
	}
	fmt.Printf("[see_image] API 调用完成, 耗时: %v, 响应大小: %d bytes\n", time.Since(startTime), len(respBody))

	// 从响应中提取文本分析结果
	analysisText, err := client.extractText(respBody)
	if err != nil {
		return "", fmt.Errorf("提取分析结果失败: %w", err)
	}

	if analysisText == "" {
		return "图片分析未返回结果", nil
	}

	result := map[string]string{
		"status":   "success",
		"analysis": analysisText,
		"source":   input.ImageSource,
	}
	resultJSON, _ := json.Marshal(result)
	return string(resultJSON), nil
}

// ============================================================
// 辅助函数
// ============================================================

// downloadImage 从 URL 下载图片，返回图片数据和 MIME 类型
func downloadImage(imageURL string) ([]byte, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "EinoImageTool/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("请求图片失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载图片失败，HTTP 状态码: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("读取图片数据失败: %w", err)
	}

	// 检测 MIME 类型
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	return data, mimeType, nil
}

// extractDataURI 解析 data:image/xxx;base64,xxxxx 格式的 URL
func extractDataURI(uri string) ([]byte, string, error) {
	idx := strings.Index(uri, "base64,")
	if idx == -1 {
		return nil, "", fmt.Errorf("data URI 格式不正确")
	}
	b64Data := uri[idx+7:]

	// 清理可能的尾部非 base64 字符
	endIdx := strings.IndexAny(b64Data, " \n\r\t\"'`)")
	if endIdx > 0 {
		b64Data = b64Data[:endIdx]
	}

	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		// 尝试 URL-safe base64
		data, err = base64.URLEncoding.DecodeString(b64Data)
		if err != nil {
			return nil, "", fmt.Errorf("base64 解码失败: %w", err)
		}
	}

	// 解析 MIME 类型
	mimeType := "image/png"
	headerPart := uri[5:idx] // "data:" 后到 "base64," 前
	if semiIdx := strings.Index(headerPart, ";"); semiIdx > 0 {
		mimeType = headerPart[:semiIdx]
	}

	ext := mimeToExt(mimeType)
	return data, ext, nil
}

// mimeToExt 将 MIME 类型转换为文件扩展名
func mimeToExt(mimeType string) string {
	switch {
	case strings.Contains(mimeType, "png"):
		return ".png"
	case strings.Contains(mimeType, "jpeg"), strings.Contains(mimeType, "jpg"):
		return ".jpg"
	case strings.Contains(mimeType, "gif"):
		return ".gif"
	case strings.Contains(mimeType, "webp"):
		return ".webp"
	case strings.Contains(mimeType, "bmp"):
		return ".bmp"
	default:
		return ".png"
	}
}

// isPNG 检查数据是否为 PNG 格式
func isPNG(data []byte) bool {
	return len(data) > 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G'
}

// isJPEG 检查数据是否为 JPEG 格式
func isJPEG(data []byte) bool {
	return len(data) > 2 && data[0] == 0xFF && data[1] == 0xD8
}

// truncateString 截断字符串
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
