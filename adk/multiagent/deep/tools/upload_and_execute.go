package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"sandbox"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// uploadAndExecuteToolInfo 定义了 upload_and_execute 工具的元信息
// 与 execute_code 的区别是：此工具支持携带输入文件，适用于需要处理数据文件的场景
var uploadAndExecuteToolInfo = &schema.ToolInfo{
	Name: "upload_and_execute",
	Desc: `在安全沙箱中执行代码，并可以携带输入文件。适用于需要处理数据文件的场景，
如 CSV 转换、JSON 验证、文本分析、Excel 处理等。
输入文件会被放置在沙箱的 /tmp 目录下，代码中可以直接通过文件名访问。
沙箱环境预装了 Python3（含 pandas、matplotlib、openpyxl）、Node.js 和常用 Linux 工具。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"language": {
			Type:     schema.String,
			Desc:     "编程语言，可选值: python, nodejs, bash",
			Required: true,
		},
		"code": {
			Type:     schema.String,
			Desc:     "要执行的代码内容（明文字符串）",
			Required: true,
		},
		"input_files": {
			Type: schema.Array,
			Desc: `输入文件列表，每个文件包含 name（文件名）和 content（文件内容，明文字符串）。
文件会被放置在沙箱的 /tmp 目录下，代码中可以直接通过文件名访问。
例如: [{"name": "data.csv", "content": "name,age\n张三,28"}]`,
			Required: true,
			ElemInfo: &schema.ParameterInfo{
				Type: schema.Object,
				Desc: "输入文件对象",
				SubParams: map[string]*schema.ParameterInfo{
					"name": {
						Type:     schema.String,
						Desc:     "文件名，如 data.csv",
						Required: true,
					},
					"content": {
						Type:     schema.String,
						Desc:     "文件内容（明文字符串）",
						Required: true,
					},
				},
			},
		},
		"products": {
			Type: schema.Array,
			Desc: `预期产出文件名列表。如果代码会生成文件，请在此指定文件名，执行后会返回文件的下载链接。
例如: ["result.csv", "report.json"]`,
			ElemInfo: &schema.ParameterInfo{
				Type: schema.String,
				Desc: "产出文件名",
			},
		},
	}),
}

// inputFile 表示 LLM 传入的单个输入文件
type inputFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// uploadAndExecuteInput 是 LLM 传入的 JSON 参数对应的 Go 结构体
type uploadAndExecuteInput struct {
	Language   string      `json:"language"`
	Code       string      `json:"code"`
	InputFiles []inputFile `json:"input_files"`
	Products   []string    `json:"products"`
}

// uploadAndExecuteTool 实现了 tool.InvokableTool 接口
type uploadAndExecuteTool struct {
	manager *sandbox.SandboxManager
}

// NewUploadAndExecuteTool 创建一个新的 upload_and_execute 工具实例
func NewUploadAndExecuteTool(manager *sandbox.SandboxManager) tool.InvokableTool {
	return &uploadAndExecuteTool{manager: manager}
}

// Info 返回工具的元信息
func (t *uploadAndExecuteTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return uploadAndExecuteToolInfo, nil
}

// InvokableRun 是工具的实际执行逻辑
func (t *uploadAndExecuteTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 1. 解析 LLM 传入的 JSON 参数
	input := &uploadAndExecuteInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}

	// 2. 校验必填参数
	if input.Language == "" {
		return "❌ 参数错误: language 不能为空，可选值: python, nodejs, bash", nil
	}
	if input.Code == "" {
		return "❌ 参数错误: code 不能为空", nil
	}
	if len(input.InputFiles) == 0 {
		return "❌ 参数错误: input_files 不能为空，如果不需要输入文件请使用 execute_code 工具", nil
	}

	// 3. 根据语言类型生成执行命令和脚本文件名
	// 这里复用了 execute_code.go 中定义的 buildCommandAndScriptName 函数
	cmd, scriptName, err := buildCommandAndScriptName(input.Language)
	if err != nil {
		return fmt.Sprintf("❌ %v", err), nil
	}

	// 4. 将输入文件转换为沙箱的 Attachment 格式
	// LLM 传入的是明文内容，但沙箱要求 base64 编码，所以这里需要转换
	var attachments []sandbox.Attachment
	for _, f := range input.InputFiles {
		if f.Name == "" {
			return "❌ 参数错误: input_files 中的 name 不能为空", nil
		}
		if f.Content == "" {
			return fmt.Sprintf("❌ 参数错误: 文件 %s 的 content 不能为空", f.Name), nil
		}
		// base64.StdEncoding.EncodeToString 将明文字节转为 base64 字符串
		attachments = append(attachments, sandbox.Attachment{
			Name:    f.Name,
			Content: base64.StdEncoding.EncodeToString([]byte(f.Content)),
		})
	}

	// 5. 构建沙箱执行请求
	req := &sandbox.ExecuteRequest{
		Cmd:           cmd,
		ScriptContent: input.Code,
		ScriptName:    scriptName,
		Products:      input.Products,
		Attachments:   attachments,
	}

	// 6. 调用沙箱执行
	result, err := t.manager.Execute(req)
	if err != nil {
		return fmt.Sprintf("❌ 沙箱执行出错: %v", err), nil
	}

	// 7. 格式化返回结果（复用 execute_code.go 中的 formatExecuteResult 函数）
	return formatUploadAndExecuteResult(result, input.InputFiles), nil
}

// formatUploadAndExecuteResult 格式化带附件执行的结果
// 在基础结果之上，额外显示上传的文件信息
func formatUploadAndExecuteResult(result *sandbox.ExecuteResult, inputFiles []inputFile) string {
	var sb strings.Builder

	// 状态行
	if result.Success {
		sb.WriteString("✅ 代码执行成功\n")
	} else {
		sb.WriteString("❌ 代码执行失败\n")
	}

	// 显示上传的文件列表
	sb.WriteString("\n📎 上传的输入文件:\n")
	for _, f := range inputFiles {
		sb.WriteString(fmt.Sprintf("- %s (%d bytes)\n", f.Name, len(f.Content)))
	}

	// 标准输出
	if result.Output != "" {
		sb.WriteString("\n📤 标准输出:\n")
		sb.WriteString(result.Output)
		if !strings.HasSuffix(result.Output, "\n") {
			sb.WriteString("\n")
		}
	}

	// 错误信息
	if result.Error != "" {
		sb.WriteString("\n📛 错误信息:\n")
		sb.WriteString(result.Error)
		if !strings.HasSuffix(result.Error, "\n") {
			sb.WriteString("\n")
		}
	}

	// 产出文件
	if len(result.Products) > 0 {
		sb.WriteString("\n📁 产出文件:\n")
		for name, item := range result.Products {
			sb.WriteString(fmt.Sprintf("- %s (%d bytes): %s\n", name, item.Size, item.URL))
		}
		sb.WriteString("\n⚠️ 注意: 产出文件下载链接有效期为 30 分钟\n")
	}

	return sb.String()
}
