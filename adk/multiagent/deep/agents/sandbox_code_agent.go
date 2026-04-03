package agents

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/cloudwego/eino-examples/adk/multiagent/deep/params"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/sandbox"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/tools"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/utils"
)

// NewSandboxCodeAgent 创建一个基于远程沙箱的代码执行 Agent
// 与 NewCodeAgent 的区别：
//   - CodeAgent 使用本地命令行工具（bash、python_runner 等）在本机执行代码
//   - SandboxCodeAgent 使用远程沙箱工具（execute_code、upload_and_execute、check_sandbox_env）
//     在隔离的云端沙箱中执行代码，更安全
//
// 参数说明：
//   - ctx: Go 的上下文对象，用于传递请求级别的信息（如超时、取消信号等）
//   - manager: 沙箱管理器，负责与腾讯云 AGS 沙箱服务通信
func NewSandboxCodeAgent(ctx context.Context, manager *sandbox.SandboxManager) (adk.Agent, error) {
	// 创建 LLM 聊天模型
	// WithMaxTokens(14125) 限制模型单次回复的最大 token 数
	// WithTemperature(1) 和 WithTopP(1) 控制模型输出的随机性（1 表示正常随机性）
	cm, err := utils.NewChatModel(ctx,
		utils.WithMaxTokens(14125),
		utils.WithTemperature(float32(1)),
		utils.WithTopP(float32(1)),
	)
	if err != nil {
		return nil, err
	}

	// 预处理器：用于修复 LLM 生成的可能不合法的 JSON 参数
	// LLM 有时会生成带有多余字符的 JSON，这个预处理器会尝试修复
	preprocess := []tools.ToolRequestPreprocess{tools.ToolRequestRepairJSON}

	// 创建三个沙箱工具实例
	// 每个工具都需要传入 manager，因为它们都需要通过 manager 来调用沙箱服务
	executeCodeTool := tools.NewExecuteCodeTool(manager)
	uploadAndExecuteTool := tools.NewUploadAndExecuteTool(manager)
	checkSandboxEnvTool := tools.NewCheckSandboxEnvTool(manager)

	// 使用 adk.NewChatModelAgent 创建一个 ReAct 模式的 Agent
	// ReAct = Reasoning + Acting，即 LLM 先思考再调用工具，循环执行直到完成任务
	sa, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		// Name: Agent 的唯一标识名称，主 Agent 通过这个名称来调度子 Agent
		Name: "SandboxCodeAgent",

		// Description: Agent 的能力描述，主 Agent 根据这个描述来决定何时调用此子 Agent
		// 这段描述非常重要，它告诉主 Agent 这个子 Agent 擅长什么
		Description: `This sub-agent executes code in a secure remote sandbox environment.
It receives a clear task and accomplishes it by generating Python/Node.js/Bash code and executing it in an isolated cloud sandbox.
The sandbox environment comes with Python3 (including pandas, matplotlib, openpyxl and other common libraries), Node.js and common Linux tools pre-installed.
It supports uploading input files and downloading output files.
The React agent should invoke this sub-agent whenever code execution in a secure sandbox is required, such as data processing, file conversion, chart generation, etc.`,

		// Instruction: 给 LLM 的系统指令，指导它如何完成任务
		Instruction: `You are a sandbox code agent. Your workflow is as follows:
1. You will be given a clear task that requires code execution.
2. If you are unsure about the sandbox environment capabilities, use the check_sandbox_env tool first.
3. Write Python/Node.js/Bash code to accomplish the task.
4. Use execute_code tool to run code directly, or upload_and_execute tool if you need to process input files.
5. If the task requires generating output files (charts, reports, processed data), specify them in the products parameter.

Available tools:
- execute_code: Execute code in the sandbox. Supports Python, Node.js, and Bash.
- upload_and_execute: Execute code with input files. Use this when you need to process data files.
- check_sandbox_env: Check the sandbox environment (installed packages, system info, etc.)

Key libraries available in the sandbox:
- pandas: for data analysis and manipulation
- matplotlib: for plotting and visualization
- openpyxl: for reading and writing Excel files
- numpy: for numerical computing

Notice:
1. Tool Calls argument must be a valid json.
2. Tool Calls argument should do not contains invalid suffix like ']<|FunctionCallEnd|>'.
3. All code runs in /tmp directory of the sandbox.
4. Output files download links are valid for 30 minutes.
`,

		// Model: 使用的 LLM 模型
		Model: cm,

		// ToolsConfig: 配置此 Agent 可以使用的工具
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				// Tools: 工具列表，每个工具都用 NewWrapTool 包装
				// NewWrapTool 的作用是在工具调用前后添加预处理和后处理逻辑
				// 参数1: 原始工具实例
				// 参数2: 请求预处理器列表（如 JSON 修复）
				// 参数3: 响应后处理器列表（这里为 nil，表示不需要后处理）
				Tools: []tool.BaseTool{
					tools.NewWrapTool(executeCodeTool, preprocess, nil),
					tools.NewWrapTool(uploadAndExecuteTool, preprocess, nil),
					tools.NewWrapTool(checkSandboxEnvTool, preprocess, nil),
				},
			},
		},

		// GenModelInput: 自定义模型输入生成函数
		// 这个函数在每次 LLM 调用前执行，用于将 Agent 的指令和用户输入组装成 LLM 的输入消息
		GenModelInput: func(ctx context.Context, instruction string, input *adk.AgentInput) ([]adk.Message, error) {
			// 从上下文中获取工作目录
			// params.GetTypedContextParams 是项目自定义的上下文参数获取方法
			wd, ok := params.GetTypedContextParams[string](ctx, params.WorkDirSessionKey)
			if !ok {
				return nil, fmt.Errorf("work dir not found")
			}

			// 使用 Jinja2 模板引擎构建提示词
			// schema.Jinja2 表示使用 Jinja2 语法（{{ variable }} 形式的模板变量）
			// schema.SystemMessage 是系统消息（设定 LLM 的角色和行为）
			// schema.UserMessage 是用户消息（包含具体的任务信息）
			tpl := prompt.FromMessages(schema.Jinja2,
				schema.SystemMessage(instruction),
				schema.UserMessage(`WorkingDirectory: {{ working_dir }}
UserQuery: {{ user_query }}
CurrentTime: {{ current_time }}
`))

			// 用实际值替换模板中的变量
			msgs, err := tpl.Format(ctx, map[string]any{
				"working_dir":  wd,
				"user_query":   utils.FormatInput(input.Messages),
				"current_time": utils.GetCurrentTime(),
			})
			if err != nil {
				return nil, err
			}

			return msgs, nil
		},

		// OutputKey: 输出键名，空字符串表示使用默认值
		OutputKey: "",

		// MaxIterations: ReAct 循环的最大迭代次数
		// 每次迭代 = LLM 思考一次 + 调用一次工具
		// 设置上限防止无限循环
		MaxIterations: 1000,
	})
	if err != nil {
		return nil, err
	}

	return sa, nil
}
