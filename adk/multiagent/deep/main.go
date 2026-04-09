/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	localbk "github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/cloudwego/eino-examples/adk/common/prints"
	"github.com/cloudwego/eino-examples/adk/common/trace"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/agents"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/generic"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/params"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/sandbox"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/tools"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/utils"
)

func main() {
	// Set your own query here. e.g.
	// query := schema.UserMessage("统计附件文件中推荐的小说名称及推荐次数，并将结果写到文件中。凡是带有《》内容都是小说名称，形成表格，表头为小说名称和推荐次数，同名小说只列一行，推荐次数相加")
	// query := schema.UserMessage("Count the recommended novel names and recommended times in the attachment file, and write the results into the file. The content with "" is the name of the novel, forming a table. The header is the name of the novel and the number of recommendations. The novels with the same name are listed in one row, and the number of recommendations is added")

	// query := schema.UserMessage("读取 模拟出题.csv 中的表格内容，规范格式将题目、答案、解析、选项放在同一行，简答题只把答案写入解析即可")
	// query := schema.UserMessage("Read the table content in the 模拟出题.csv, put the question, answer, resolution and options in the same line in a standardized format, and simply write the answer into the resolution")

	// query := schema.UserMessage("请帮我将 questions.csv 表格中的第一列提取到一个新的 csv 中")
	// query := schema.UserMessage("Please help me extract the first column in question.csv table into a new csv")
	//query := schema.UserMessage("请帮我搜索明天深圳天气")
	//query := schema.UserMessage("请帮我在网上搜索明天深圳天气")
	//query := schema.UserMessage("请帮我查询腾讯最新新闻")
	//query := schema.UserMessage("请帮我总结知识内容：https://golang-china.github.io/gopl-zh/ch8/ch8-01.html")
	//query := schema.UserMessage("请帮我在网上搜索并总结昆明4月旅游注意事项")
	//query := schema.UserMessage("请在沙箱中给出一个快速排序的算法，并给出一个测试用例的排序结果")
	//query := schema.UserMessage("请帮我查询深圳的地理位置信息，当前深圳的时间，以及深圳今天的天气情况")

	//query := schema.UserMessage("请帮我生成一只小猫的图片")
	//query := schema.UserMessage("请帮把除了小猫之外的所有背景扣掉，file:///Users/earky/Downloads/1290a524-c92f-4da6-89d7-5372c704b066.png")
	//query := schema.UserMessage("请帮我分析该图片，<COS图片URL>")
	query := schema.UserMessage("你现在有哪些 Skill？")
	ctx := context.Background()

	traceCloseFn, startSpanFn := trace.AppendCozeLoopCallbackIfConfigured(ctx)
	defer traceCloseFn(ctx)

	agent, err := newExcelAgent(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// uuid as task id
	id := uuid.New().String()

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
	})

	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	var inputFileDir, workdir string
	if env := os.Getenv("EXCEL_AGENT_INPUT_DIR"); env != "" {
		inputFileDir = env
	} else {
		inputFileDir = filepath.Join(wd, "adk/multiagent/deep/playground/input")
	}

	if env := os.Getenv("EXCEL_AGENT_WORK_DIR"); env != "" {
		workdir = filepath.Join(env, id)
	} else {
		workdir = filepath.Join(wd, "adk/multiagent/deep/playground", id)
	}

	if err = os.Mkdir(workdir, 0o755); err != nil {
		log.Fatal(err)
	}

	if err = os.CopyFS(workdir, os.DirFS(inputFileDir)); err != nil {
		log.Fatal(err)
	}

	previews, err := generic.PreviewPath(workdir)
	if err != nil {
		log.Fatal(err)
	}

	ctx = params.InitContextParams(ctx)
	params.AppendContextParams(ctx, map[string]interface{}{
		params.FilePathSessionKey:            inputFileDir,
		params.WorkDirSessionKey:             workdir,
		params.UserAllPreviewFilesSessionKey: utils.ToJSONString(previews),
		params.TaskIDKey:                     id,
	})

	ctx, endSpanFn := startSpanFn(ctx, "plan-execute-replan", query)

	iter := runner.Run(ctx, []*schema.Message{query})

	var (
		lastMessage       adk.Message
		lastMessageStream *schema.StreamReader[adk.Message]
	)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			if lastMessageStream != nil {
				lastMessageStream.Close()
			}
			if event.Output.MessageOutput.IsStreaming {
				cpStream := event.Output.MessageOutput.MessageStream.Copy(2)
				event.Output.MessageOutput.MessageStream = cpStream[0]
				lastMessage = nil
				lastMessageStream = cpStream[1]
			} else {
				lastMessage = event.Output.MessageOutput.Message
				lastMessageStream = nil
			}
		}
		prints.Event(event)
	}

	if lastMessage != nil {
		endSpanFn(ctx, lastMessage)
	} else if lastMessageStream != nil {
		msg, _ := schema.ConcatMessageStream(lastMessageStream)
		endSpanFn(ctx, msg)
	} else {
		endSpanFn(ctx, "finished without output message")
	}

	time.Sleep(time.Second * 30)
}

func newExcelAgent(ctx context.Context) (adk.Agent, error) {
	operator := &LocalOperator{}

	cm, err := utils.NewChatModel(ctx,
		utils.WithMaxTokens(4096),
		utils.WithTemperature(float32(0)),
		utils.WithTopP(float32(0)),
	)
	if err != nil {
		return nil, err
	}

	ca, err := agents.NewCodeAgent(ctx, operator)
	if err != nil {
		return nil, err
	}
	wa, err := agents.NewWebSearchAgent(ctx)
	if err != nil {
		return nil, err
	}

	// 创建网页抓取 Agent
	wfa, err := agents.NewWebFetchAgent(ctx)
	if err != nil {
		return nil, err
	}

	// 创建实用工具 Agent（天气、时间、位置查询）
	ua, err := agents.NewUtilityAgent(ctx)
	if err != nil {
		return nil, err
	}

	// 创建图像处理 Agent（图片生成、编辑、查看分析）
	var ia adk.Agent
	ia, err = agents.NewImageAgent(ctx)
	if err != nil {
		log.Printf("⚠️ 图像处理 Agent 创建失败（ImageAgent 将不可用）: %v", err)
		ia = nil
	}

	// 创建沙箱管理器（从配置文件加载，敏感信息通过环境变量注入）
	// sandbox.NewSandboxManagerFromConfigFile 会读取 config.yaml，然后用环境变量覆盖敏感字段
	sandboxConfigPath := filepath.Join("adk/multiagent/deep/sandbox/config.yaml")
	sbManager, err := sandbox.NewSandboxManagerFromConfigFile(sandboxConfigPath)
	if err != nil {
		log.Printf("⚠️ 沙箱管理器创建失败（SandboxCodeAgent 将不可用）: %v", err)
	}

	// 创建沙箱代码 Agent（如果沙箱管理器创建成功）
	var sca adk.Agent
	if sbManager != nil {
		sca, err = agents.NewSandboxCodeAgent(ctx, sbManager)
		if err != nil {
			return nil, err
		}
	}

	// 构建子 Agent 列表
	// 始终包含 CodeAgent、WebSearchAgent、WebFetchAgent 和 UtilityAgent
	// 如果沙箱可用则额外添加 SandboxCodeAgent，如果图像处理可用则额外添加 ImageAgent
	subAgents := []adk.Agent{ca, wa, wfa, ua}
	if sca != nil {
		subAgents = append(subAgents, sca)
	}
	if ia != nil {
		subAgents = append(subAgents, ia)
	}

	// 构建 Skill 中间件（可选）
	// 默认使用 eino-ext 的四个 Skill（eino-guide/eino-component/eino-compose/eino-agent）
	// 可通过环境变量 EINO_EXT_SKILLS_DIR 覆盖 skills 目录路径
	var handlers []adk.ChatModelAgentMiddleware
	skillsDir, skillsFound := resolveSkillsDir()
	if skillsFound {
		backend, backendErr := localbk.NewBackend(ctx, &localbk.Config{})
		if backendErr != nil {
			log.Printf("⚠️ Skill backend 创建失败（Skill 将不可用）: %v", backendErr)
		} else {
			skillBackend, sbErr := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
				Backend: backend,
				BaseDir: skillsDir,
			})
			if sbErr != nil {
				log.Printf("⚠️ Skill backend from filesystem 创建失败（Skill 将不可用）: %v", sbErr)
			} else {
				skillMiddleware, smErr := skill.NewMiddleware(ctx, &skill.Config{
					Backend: skillBackend,
				})
				if smErr != nil {
					log.Printf("⚠️ Skill 中间件创建失败（Skill 将不可用）: %v", smErr)
				} else {
					handlers = append(handlers, skillMiddleware)
					log.Printf("✅ Skill 中间件已启用，skills 目录: %s", skillsDir)
				}
			}
		}
	} else {
		log.Println("ℹ️ Skill 未配置（设置 EINO_EXT_SKILLS_DIR 环境变量可启用）")
	}
	// 安全工具中间件放在最后，确保能捕获所有工具（包括 Skill 工具）的错误
	handlers = append(handlers, &safeToolMiddleware{})

	// 定义了主 Agent
	deepAgent, err := deep.New(ctx, &deep.Config{
		Name:        "ExcelAgent",
		Description: "an agent for excel task",
		ChatModel:   cm,
		// 注册子 Agent
		SubAgents: subAgents,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				// 主 Agent 可用的工具
				Tools: []tool.BaseTool{
					tools.NewWrapTool(tools.NewReadFileTool(operator), nil, nil),
					tools.NewWrapTool(tools.NewTreeTool(operator), nil, nil),
				},
			},
		},
		MaxIteration: 100,
		// Skill 中间件 + 安全工具中间件
		Handlers: handlers,
		// 模型调用重试配置：遇到临时性错误时自动重试，最多重试 5 次（指数退避）
		ModelRetryConfig: &adk.ModelRetryConfig{
			MaxRetries: 5,
			IsRetryAble: func(_ context.Context, err error) bool {
				if err == nil {
					return false
				}
				errMsg := err.Error()
				// 将错误信息转为小写，方便统一匹配
				lowerMsg := strings.ToLower(errMsg)

				shouldRetry := false
				reason := ""

				switch {
				// 1. HTTP 429 限流错误（最常见）
				case strings.Contains(errMsg, "429") ||
					strings.Contains(lowerMsg, "too many requests") ||
					strings.Contains(lowerMsg, "qpm limit") ||
					strings.Contains(lowerMsg, "rate limit") ||
					strings.Contains(lowerMsg, "rate_limit") ||
					strings.Contains(lowerMsg, "throttl"):
					shouldRetry = true
					reason = "限流(429/rate limit)"

				// 2. HTTP 503 服务暂时不可用
				case strings.Contains(errMsg, "503") ||
					strings.Contains(lowerMsg, "service unavailable") ||
					strings.Contains(lowerMsg, "service temporarily unavailable"):
					shouldRetry = true
					reason = "服务暂时不可用(503)"

				// 3. HTTP 502 网关错误
				case strings.Contains(errMsg, "502") ||
					strings.Contains(lowerMsg, "bad gateway"):
					shouldRetry = true
					reason = "网关错误(502)"

				// 4. 超时错误
				case strings.Contains(lowerMsg, "timeout") ||
					strings.Contains(lowerMsg, "deadline exceeded") ||
					strings.Contains(lowerMsg, "context deadline"):
					shouldRetry = true
					reason = "请求超时"

				// 5. 连接错误（网络抖动）
				case strings.Contains(lowerMsg, "connection reset") ||
					strings.Contains(lowerMsg, "connection refused") ||
					strings.Contains(lowerMsg, "broken pipe") ||
					strings.Contains(lowerMsg, "eof"):
					shouldRetry = true
					reason = "连接错误"

				// 6. 服务端内部错误（500，谨慎重试）
				case strings.Contains(errMsg, "500") &&
					strings.Contains(lowerMsg, "internal server error"):
					shouldRetry = true
					reason = "服务端内部错误(500)"
				}

				if shouldRetry {
					log.Printf("⚠️ 模型调用失败，准备重试 | 原因: %s | 错误: %s", reason, errMsg)
				}
				return shouldRetry
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return deepAgent, nil
}

// resolveSkillsDir 解析 Skill 目录路径
// 优先使用环境变量 EINO_EXT_SKILLS_DIR，否则使用默认的 eino-ext skills 路径
func resolveSkillsDir() (string, bool) {
	// 优先从环境变量获取
	skillsDir := strings.TrimSpace(os.Getenv("EINO_EXT_SKILLS_DIR"))
	if skillsDir == "" {
		// 默认使用 chatwitheino 项目下的 eino-ext skills
		skillsDir = "/Users/zes/work/eino-examples/quickstart/chatwitheino/skills/eino-ext"
	}
	if absDir, err := filepath.Abs(skillsDir); err == nil {
		skillsDir = absDir
	}
	fi, err := os.Stat(skillsDir)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	return skillsDir, true
}

// safeToolMiddleware 安全工具中间件
// 捕获工具调用过程中的所有错误（如文件找不到、网络异常、参数错误等），
// 将错误转为 "[tool error] xxx" 格式的字符串返回给模型，
// 让模型可以根据错误信息调整策略，而不是直接中断整个 Agent 流程。
type safeToolMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
}

func (m *safeToolMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		result, err := endpoint(ctx, args, opts...)
		if err != nil {
			// 中断错误需要继续传播，不能拦截
			if _, ok := compose.IsInterruptRerunError(err); ok {
				return "", err
			}
			log.Printf("⚠️ 工具调用失败，已转为提示信息 | 错误: %v", err)
			return fmt.Sprintf("[tool error] %v", err), nil
		}
		return result, nil
	}, nil
}

func (m *safeToolMiddleware) WrapStreamableToolCall(
	_ context.Context,
	endpoint adk.StreamableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		sr, err := endpoint(ctx, args, opts...)
		if err != nil {
			// 中断错误需要继续传播，不能拦截
			if _, ok := compose.IsInterruptRerunError(err); ok {
				return nil, err
			}
			log.Printf("⚠️ 流式工具调用失败，已转为提示信息 | 错误: %v", err)
			return singleChunkReader(fmt.Sprintf("[tool error] %v", err)), nil
		}
		return safeWrapReader(sr), nil
	}, nil
}

// singleChunkReader 创建一个只发送一条消息的 StreamReader
func singleChunkReader(msg string) *schema.StreamReader[string] {
	r, w := schema.Pipe[string](1)
	_ = w.Send(msg, nil)
	w.Close()
	return r
}

// safeWrapReader 包装流式读取器，将流中的错误转为错误提示消息
func safeWrapReader(sr *schema.StreamReader[string]) *schema.StreamReader[string] {
	r, w := schema.Pipe[string](64)
	go func() {
		defer w.Close()
		for {
			chunk, err := sr.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				log.Printf("⚠️ 流式工具读取错误: %v", err)
				_ = w.Send(fmt.Sprintf("\n[tool error] %v", err), nil)
				return
			}
			_ = w.Send(chunk, nil)
		}
	}()
	return r
}
