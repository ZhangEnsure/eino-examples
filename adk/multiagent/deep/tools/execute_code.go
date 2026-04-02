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

// executeCodeToolInfo 定义了 execute_code 工具的元信息
// LLM 会根据这些信息来决定何时调用此工具、传入什么参数
var executeCodeToolInfo = &schema.ToolInfo{
	Name: "execute_code",
	Desc: `在安全沙箱中执行代码并返回结果。支持 Python、Node.js、Bash 等语言。
沙箱环境预装了 Python3（含 pandas、matplotlib、openpyxl 等常用库）、Node.js 和常用 Linux 工具。
代码在隔离环境中执行，执行超时时间为 5 分钟。
如果需要生成文件（如图表、报告），请在 products 参数中指定产出文件名。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"language": {
			Type:     schema.String,
			Desc:     "编程语言，可选值: python, nodejs, bash",
			Required: true,
		},
		"code": {
			Type:     schema.String,
			Desc:     "要执行的代码内容（明文字符串，不需要 base64 编码）",
			Required: true,
		},
		"products": {
			Type: schema.Array,
			Desc: `预期产出文件名列表。如果代码会生成文件，请在此指定文件名，执行后会返回文件的下载链接。
例如: ["output.csv", "chart.svg"]`,
			ElemInfo: &schema.ParameterInfo{
				Type: schema.String,
				Desc: "产出文件名",
			},
		},
	}),
}

// executeCodeInput 是 LLM 传入的 JSON 参数对应的 Go 结构体
// json tag 必须和 ToolInfo 中的参数名一致，这样 json.Unmarshal 才能正确解析
type executeCodeInput struct {
	Language string   `json:"language"`
	Code     string   `json:"code"`
	Products []string `json:"products"`
}

// executeCodeTool 实现了 tool.InvokableTool 接口
// 它持有一个 SandboxManager 的引用，用于调用沙箱执行代码
type executeCodeTool struct {
	manager *sandbox.SandboxManager
}

// NewExecuteCodeTool 创建一个新的 execute_code 工具实例
// 参数 manager 是沙箱管理器，由调用方创建并传入
func NewExecuteCodeTool(manager *sandbox.SandboxManager) tool.InvokableTool {
	return &executeCodeTool{manager: manager}
}

// Info 返回工具的元信息，LLM 通过这些信息了解工具的名称、描述和参数
// 这是 tool.BaseTool 接口要求的方法
func (t *executeCodeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return executeCodeToolInfo, nil
}

// InvokableRun 是工具的实际执行逻辑
// 当 LLM 决定调用此工具时，框架会将 LLM 生成的 JSON 参数传入 argumentsInJSON
// 返回值是字符串，会作为工具调用结果返回给 LLM
func (t *executeCodeTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 1. 解析 LLM 传入的 JSON 参数
	input := &executeCodeInput{}
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

	// 3. 根据语言类型生成执行命令和脚本文件名
	cmd, scriptName, err := buildCommandAndScriptName(input.Language)
	if err != nil {
		return fmt.Sprintf("❌ %v", err), nil
	}

	// 4. 构建沙箱执行请求
	req := &sandbox.ExecuteRequest{
		Cmd:           cmd,
		ScriptContent: input.Code,
		ScriptName:    scriptName,
		Products:      input.Products,
	}

	// 5. 调用沙箱执行
	result, err := t.manager.Execute(req)
	if err != nil {
		return fmt.Sprintf("❌ 沙箱执行出错: %v", err), nil
	}

	// 6. 格式化返回结果，让 LLM 能理解执行情况
	return formatExecuteResult(result), nil
}

// buildCommandAndScriptName 根据语言类型生成对应的执行命令和脚本文件名
// 例如: python -> ("python3 script.py", "script.py", nil)
func buildCommandAndScriptName(language string) (cmd, scriptName string, err error) {
	// strings.ToLower 将字符串转为小写，避免 LLM 传入 "Python" 或 "PYTHON" 时出错
	switch strings.ToLower(language) {
	case "python", "python3", "py":
		return "python3 script.py", "script.py", nil
	case "nodejs", "node", "javascript", "js":
		return "node script.js", "script.js", nil
	case "bash", "shell", "sh":
		return "bash script.sh", "script.sh", nil
	default:
		return "", "", fmt.Errorf("不支持的语言: %s，可选值: python, nodejs, bash", language)
	}
}

// formatExecuteResult 将沙箱执行结果格式化为 LLM 易于理解的字符串
func formatExecuteResult(result *sandbox.ExecuteResult) string {
	var sb strings.Builder

	// 状态行
	if result.Success {
		sb.WriteString("✅ 代码执行成功\n")
	} else {
		sb.WriteString("❌ 代码执行失败\n")
	}

	// 标准输出
	if result.Output != "" {
		sb.WriteString("\n📤 标准输出:\n")
		sb.WriteString(result.Output)
		// 确保输出末尾有换行
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

// toBase64 将字符串编码为 base64
// base64 是一种将二进制数据转为纯文本的编码方式，常用于网络传输
func toBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
