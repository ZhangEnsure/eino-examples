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
	"bufio"
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

	// ========== 多轮对话循环 ==========
	// 仿照 ch02 的方式，维护 history 对话历史，循环读取用户输入
	history := make([]*schema.Message, 0, 16)
	scanner := bufio.NewScanner(os.Stdin)
	// 设置较大的缓冲区，避免长输入被截断
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	fmt.Println("========================================")
	fmt.Println("  Deep Agent 多轮对话模式")
	fmt.Println("  输入问题开始对话，输入空行退出")
	fmt.Println("========================================")

	for {
		fmt.Print("\nyou> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Println("对话结束，再见！")
			break
		}

		// 1. 把用户输入包装成 UserMessage，追加到 history
		query := schema.UserMessage(line)
		history = append(history, query)

		// 2. 为每轮对话创建 trace span
		spanCtx, endSpanFn := startSpanFn(ctx, "plan-execute-replan", query)

		// 3. 调用 Runner 执行 Agent，传入完整的对话历史
		iter := runner.Run(spanCtx, history)

		// 4. 消费事件流，打印并收集 AI 的回复文本
		assistantContent := consumeEventsAndCollect(iter)

		// 5. 结束 trace span
		if assistantContent != "" {
			endSpanFn(spanCtx, schema.AssistantMessage(assistantContent, nil))
		} else {
			endSpanFn(spanCtx, "finished without output message")
		}

		// 6. 把本轮 assistant 回复追加到 history，供下一轮使用
		if assistantContent != "" {
			history = append(history, schema.AssistantMessage(assistantContent, nil))
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("读取输入错误: %v", err)
	}

	// 等待异步 trace 上报完成
	time.Sleep(time.Second * 5)
}

// consumeEventsAndCollect 消费事件流，使用 prints.Event 打印事件，
// 同时收集最终的 assistant 回复文本用于维护多轮对话历史。
//
// 核心难点：prints.Event 内部会消费流式 MessageStream（调用 Recv），
// 所以不能在 prints.Event 之后再从同一个 stream 读取。
// 解决方案：对流式消息使用 Copy(2) 复制出两份 stream，
// 一份给 prints.Event 打印，另一份用于收集文本。
func consumeEventsAndCollect(iter *adk.AsyncIterator[*adk.AgentEvent]) string {
	var (
		// lastMessage 保存最后一条非流式的 assistant 消息
		lastMessage adk.Message
		// lastMessageStream 保存最后一条流式消息的副本（用于收集文本）
		lastMessageStream *schema.StreamReader[adk.Message]
	)

	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			// 关闭上一轮未消费完的流（避免 goroutine 泄漏）
			if lastMessageStream != nil {
				lastMessageStream.Close()
			}
			if event.Output.MessageOutput.IsStreaming {
				// Copy(2) 是 schema.StreamReader 的方法，将一个 stream 复制为 N 个独立的 stream，
				// 每个副本都能独立消费完整的数据流。
				// cpStream[0] 给 prints.Event 打印，cpStream[1] 留给我们收集文本。
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

	// 从最后一条消息中提取 assistant 回复文本
	if lastMessage != nil {
		return lastMessage.Content
	}
	if lastMessageStream != nil {
		// ConcatMessageStream 将流式消息的所有 chunk 拼接为一条完整的 Message
		msg, err := schema.ConcatMessageStream(lastMessageStream)
		if err != nil {
			log.Printf("⚠️ 收集流式回复失败: %v", err)
			return ""
		}
		if msg != nil {
			return msg.Content
		}
	}
	return ""
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
