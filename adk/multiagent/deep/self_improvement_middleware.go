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
	"log"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// selfImprovementMiddleware 实现 self-improvement 的两个 Hook：
//
// 1. BeforeModelRewriteState（对应 UserPromptSubmit / activator.sh）：
//    每次模型调用前，向 Messages 末尾注入一条 <self-improvement-reminder> 提醒，
//    引导 Agent 在完成任务后评估是否有可提取的学习。
//
// 2. WrapInvokableToolCall / WrapStreamableToolCall（对应 PostToolUse/Bash / error-detector.sh）：
//    工具执行后，检测输出中是否包含错误模式（error:、failed、Permission denied 等），
//    如果匹配则在工具结果末尾追加 <error-detected> 提醒，引导 Agent 记录 [ERR] 日记条目。
//    注意：只对「执行类」工具进行错误检测，跳过「文档类」工具（如 skill、diary_search），
//    因为文档内容中自然包含错误关键词（如 SKILL.md 中的示例），会导致误报。
//
// 通过 selfImprovementConfig 可以按需选择启用哪些 Hook：
//   - EnableReminder: 启用 <self-improvement-reminder> 注入（默认 true）
//   - EnableErrorDetection: 启用 <error-detected> 错误检测（默认 true）
type selfImprovementMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	config selfImprovementConfig
}

// selfImprovementConfig 控制 self-improvement 中间件的行为
type selfImprovementConfig struct {
	// EnableReminder 是否启用 <self-improvement-reminder> 注入
	// 对应 hooks-setup.md 中的 UserPromptSubmit hook (activator.sh)
	// 每次模型调用前注入学习提醒，开销约 50-100 tokens
	EnableReminder bool

	// EnableErrorDetection 是否启用 <error-detected> 错误检测
	// 对应 hooks-setup.md 中的 PostToolUse hook (error-detector.sh)
	// 工具执行后检测错误模式并注入提醒
	EnableErrorDetection bool
}

// defaultSelfImprovementConfig 返回默认配置（两个 Hook 都启用）
func defaultSelfImprovementConfig() selfImprovementConfig {
	return selfImprovementConfig{
		EnableReminder:       true,
		EnableErrorDetection: true,
	}
}

// errorDetectionSkipTools 是不需要进行错误检测的工具名称集合
// 这些工具返回的是文档/搜索结果，内容中自然包含错误关键词，不应触发 <error-detected>
var errorDetectionSkipTools = map[string]bool{
	"skill":        true, // Skill 工具返回 SKILL.md 文档内容
	"diary_search": true, // 日记搜索返回历史日记条目（可能包含 [ERR] 条目）
}

// selfImprovementReminder 是每次模型调用前注入的提醒内容
// 对应 activator.sh 的输出，保持 ~50-100 tokens 的轻量级
const selfImprovementReminder = `<self-improvement-reminder>
After completing this task, evaluate if extractable knowledge emerged:
- Non-obvious solution discovered through investigation?
- Workaround for unexpected behavior?
- Project-specific pattern learned?
- Error required debugging to resolve?
- Completed significant work? Pause and self-reflect with a [REF] entry.

If yes: Log to today's diary (diary/YYYY-MM-DD.md) using [LRN]/[ERR]/[FEAT]/[REF] format.
If high-value (recurring, broadly applicable): Promote to SOUL.md, AGENTS.md, TOOLS.md, or MEMORY.md.
If user stated a preference explicitly: Promote immediately — no counting needed.
</self-improvement-reminder>`

// errorDetectedReminder 是检测到工具错误后追加的提醒内容
// 对应 error-detector.sh 的输出
const errorDetectedReminder = `
<error-detected>
A command error was detected. Consider logging an [ERR] entry to today's diary (diary/YYYY-MM-DD.md) if:
- The error was unexpected or non-obvious
- It required investigation to resolve
- It might recur in similar contexts
- The solution could benefit future sessions

Format: ### YYYY-MM-DD HH:MM - [ERR] Brief description
</error-detected>`

// errorPatterns 是用于检测工具输出中错误的模式列表
// 对应 error-detector.sh 中的 ERROR_PATTERNS
var errorPatterns = []string{
	"error:",
	"Error:",
	"ERROR:",
	"failed",
	"FAILED",
	"command not found",
	"No such file",
	"Permission denied",
	"fatal:",
	"Exception",
	"Traceback",
	"npm ERR!",
	"ModuleNotFoundError",
	"SyntaxError",
	"TypeError",
	"exit code",
	"non-zero",
}

// BeforeModelRewriteState 在每次模型调用前注入 self-improvement 提醒
// 对应 self-improvement 的 UserPromptSubmit hook (activator.sh)
//
// 实现策略：将 <self-improvement-reminder> 追加到最后一条 user 消息的 content 末尾。
// 相比在末尾追加独立的 system 消息，这种方式有以下优势：
//   1. 模型兼容性：不引入额外的 system 消息，避免部分模型对 system 消息位置的限制
//   2. 注意力效果：reminder 紧跟用户问题，模型更容易关注
//   3. 防止 ReAct 累积：只在最后一条是 user 消息时注入，工具调用回合（最后一条是 tool）不会触发
//
// 防重复注入机制：
//   - 注入前先扫描所有 user 消息，清理已有的 <self-improvement-reminder> 标记
//   - 这样即使 compose.State 累积了上一轮修改过的 user 消息，也会被清理后重新注入
//   - 保证整个 messages 序列中始终只有一份 reminder
func (m *selfImprovementMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	mc *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	// 如果未启用 reminder，直接跳过
	if !m.config.EnableReminder {
		return ctx, state, nil
	}

	// 只在有消息时处理
	if len(state.Messages) == 0 {
		return ctx, state, nil
	}

	// 检查最后一条消息是否是用户消息（只在用户提问回合注入，工具调用回合不注入）
	lastMsg := state.Messages[len(state.Messages)-1]
	if lastMsg.Role != schema.User {
		log.Printf("🔄 Self-improvement reminder skipped | 最后一条消息角色为 %s（非 user），不注入 | 消息总数: %d", lastMsg.Role, len(state.Messages))
		return ctx, state, nil
	}

	// 创建新的 state 副本，避免修改原始 state
	newState := *state
	newState.Messages = make([]adk.Message, len(state.Messages))
	copy(newState.Messages, state.Messages)

	// 第一步：清理所有 user 消息中已有的 reminder（防止 ReAct 循环累积）
	cleaned := 0
	for i, msg := range newState.Messages {
		if msg.Role == schema.User && strings.Contains(msg.Content, selfImprovementReminderTag) {
			// 深拷贝这条消息，避免修改原始 state 中的指针
			msgCopy := *msg
			msgCopy.Content = stripSelfImprovementReminder(msg.Content)
			newState.Messages[i] = &msgCopy
			cleaned++
		}
	}

	// 第二步：在最后一条 user 消息的 content 末尾追加 reminder
	lastIdx := len(newState.Messages) - 1
	lastMsgCopy := *newState.Messages[lastIdx]
	originalContent := lastMsgCopy.Content
	lastMsgCopy.Content = lastMsgCopy.Content + "\n\n" + selfImprovementReminder
	newState.Messages[lastIdx] = &lastMsgCopy

	// ========== 详细日志输出 ==========
	log.Println("╔══════════════════════════════════════════════════════════════")
	if cleaned > 0 {
		log.Printf("║ 🔄 Self-improvement reminder injected (清理了 %d 条旧 reminder)", cleaned)
	} else {
		log.Println("║ 🔄 Self-improvement reminder injected")
	}
	log.Printf("║ 消息总数: %d", len(newState.Messages))

	// 输出注入前的完整消息序列（每条消息的索引、角色、内容摘要）
	log.Println("║")
	log.Println("║ 📋 注入前消息序列:")
	for i, msg := range newState.Messages {
		content := msg.Content
		if i == lastIdx {
			// 最后一条 user 消息显示原始内容（注入前）
			content = originalContent
		}
		preview := truncateContent(content, 120)
		marker := "  "
		if i == lastIdx {
			marker = "→ " // 标记被修改的消息
		}
		log.Printf("║   %s[%d] %-9s | %s", marker, i, msg.Role, preview)
	}

	// 输出被修改的 user 消息的完整内容（注入前 vs 注入后）
	log.Println("║")
	log.Println("║ 📝 被修改的 user 消息 (注入前):")
	for _, line := range strings.Split(originalContent, "\n") {
		log.Printf("║   < %s", line)
	}
	log.Println("║")
	log.Println("║ 📝 被修改的 user 消息 (注入后):")
	for _, line := range strings.Split(lastMsgCopy.Content, "\n") {
		log.Printf("║   > %s", line)
	}
	log.Println("╚══════════════════════════════════════════════════════════════")

	return ctx, &newState, nil
}

// WrapInvokableToolCall 包装同步工具调用，在工具执行后检测错误并注入提醒
// 对应 self-improvement 的 PostToolUse hook (error-detector.sh)
//
// 实现策略：对所有工具（不仅限于 bash）的执行结果进行错误模式检测，
// 如果匹配则在结果末尾追加 <error-detected> 提醒。
func (m *selfImprovementMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	// 如果未启用错误检测，直接返回原始 endpoint，零开销
	if !m.config.EnableErrorDetection {
		return endpoint, nil
	}

	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		result, err := endpoint(ctx, args, opts...)
		if err != nil {
			return result, err
		}

		// 跳过文档类工具的错误检测（避免文档内容中的错误关键词导致误报）
		if errorDetectionSkipTools[tCtx.Name] {
			return result, nil
		}

		// 检测工具输出中是否包含错误模式
		if containsErrorPattern(result) {
			log.Printf("⚠️ Self-improvement: 检测到工具 '%s' 输出中包含错误模式 | 结果前200字符: %.200s", tCtx.Name, result)
			result += errorDetectedReminder
		}

		return result, nil
	}, nil
}

// WrapStreamableToolCall 包装流式工具调用，在工具执行后检测错误并注入提醒
// 注意：流式工具的错误检测在 safeWrapReader 之后进行，
// 这里只处理初始错误（endpoint 返回 error 的情况由 safeToolMiddleware 处理）
func (m *selfImprovementMiddleware) WrapStreamableToolCall(
	_ context.Context,
	endpoint adk.StreamableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {
	// 如果未启用错误检测，直接返回原始 endpoint，零开销
	if !m.config.EnableErrorDetection {
		return endpoint, nil
	}

	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		sr, err := endpoint(ctx, args, opts...)
		if err != nil {
			return sr, err
		}

		// 跳过文档类工具的错误检测（避免文档内容中的错误关键词导致误报）
		if errorDetectionSkipTools[tCtx.Name] {
			return sr, nil
		}

		// 对流式结果进行包装，在流结束时检测错误
		return wrapStreamWithErrorDetection(sr, tCtx.Name), nil
	}, nil
}

// selfImprovementReminderTag 用于标识 reminder 的开始标签，用于清理已注入的 reminder
const selfImprovementReminderTag = "<self-improvement-reminder>"

// selfImprovementReminderEndTag 用于标识 reminder 的结束标签
const selfImprovementReminderEndTag = "</self-improvement-reminder>"

// stripSelfImprovementReminder 从文本中移除已注入的 <self-improvement-reminder>...</self-improvement-reminder> 块
// 包括块前面可能存在的空行
func stripSelfImprovementReminder(content string) string {
	startIdx := strings.Index(content, selfImprovementReminderTag)
	if startIdx < 0 {
		return content
	}
	endIdx := strings.Index(content, selfImprovementReminderEndTag)
	if endIdx < 0 {
		// 只找到开始标签没找到结束标签，截断到开始标签之前
		return strings.TrimRight(content[:startIdx], "\n ")
	}
	// 移除从开始标签到结束标签（含）的整个块，以及前面的空行
	before := strings.TrimRight(content[:startIdx], "\n ")
	after := content[endIdx+len(selfImprovementReminderEndTag):]
	return before + after
}

// truncateContent 截取内容摘要，超过 maxLen 的部分用 "..." 替代
// 同时将换行符替换为 ↵ 以便在单行日志中显示
func truncateContent(content string, maxLen int) string {
	// 将换行符替换为可见字符，方便在日志中单行显示
	s := strings.ReplaceAll(content, "\n", "↵")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// containsErrorPattern 检查文本中是否包含错误模式
func containsErrorPattern(text string) bool {
	for _, pattern := range errorPatterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

// wrapStreamWithErrorDetection 包装流式读取器，在流结束时检测错误并追加提醒
func wrapStreamWithErrorDetection(sr *schema.StreamReader[string], toolName string) *schema.StreamReader[string] {
	r, w := schema.Pipe[string](64)
	go func() {
		defer w.Close()
		var sb strings.Builder
		for {
			chunk, err := sr.Recv()
			if err != nil {
				// EOF 或其他错误，检查累积的内容是否包含错误模式
				if sb.Len() > 0 && containsErrorPattern(sb.String()) {
					log.Printf("⚠️ Self-improvement: 检测到流式工具 '%s' 输出中包含错误模式", toolName)
					_ = w.Send(errorDetectedReminder, nil)
				}
				if err.Error() != "EOF" {
					// 非 EOF 错误，继续传播
					_ = w.Send(chunk, err)
				}
				return
			}
			sb.WriteString(chunk)
			_ = w.Send(chunk, nil)
		}
	}()
	return r
}
