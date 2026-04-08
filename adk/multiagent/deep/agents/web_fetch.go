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

package agents

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/cloudwego/eino-examples/adk/multiagent/deep/tools"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/utils"
)

// NewWebFetchAgent 创建一个网页抓取 Agent
// 该 Agent 使用 ReAct 模式，通过 webfetch 工具获取指定 URL 的网页内容
// 适用于需要访问特定网页并提取信息的场景（与 WebSearchAgent 的区别在于：
// WebSearchAgent 通过搜索引擎搜索关键词，WebFetchAgent 直接抓取指定 URL 的内容）
func NewWebFetchAgent(ctx context.Context) (adk.Agent, error) {
	// 创建 LLM 聊天模型，使用默认参数
	cm, err := utils.NewChatModel(ctx)
	if err != nil {
		return nil, err
	}

	// 创建 webfetch 工具实例
	// tools.NewWebFetchTool() 返回一个实现了 tool.InvokableTool 接口的实例
	// 该工具可以通过 HTTP GET 请求抓取指定 URL 的网页内容
	fetchTool := tools.NewWebFetchTool()

	// 使用 adk.NewChatModelAgent 创建一个 ReAct 模式的 Agent
	// 返回值类型为 (adk.Agent, error)，adk.Agent 是一个接口
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		// Name: Agent 的唯一标识名称，主 Agent 通过这个名称来调度子 Agent
		Name: "WebFetchAgent",

		// Description: Agent 的能力描述，主 Agent 根据这个描述来决定何时调用此子 Agent
		// 这段描述非常重要，它告诉主 Agent 这个子 Agent 擅长什么
		Description: `WebFetchAgent is specialized in fetching and extracting content from specific web pages by URL.
It can retrieve HTML content from any given URL and help analyze or summarize the page content.
Use this agent when you have a specific URL to visit and need to extract information from that page.
Unlike WebSearchAgent which searches by keywords, this agent directly accesses a known URL.`,

		// Model: 使用的 LLM 模型
		Model: cm,

		// ToolsConfig: 配置此 Agent 可以使用的工具
		// adk.ToolsConfig 嵌套了 compose.ToolsNodeConfig，后者包含 Tools 列表
		// Tools 的类型是 []tool.BaseTool，而 tool.InvokableTool 实现了 tool.BaseTool 接口
		// 因此 fetchTool 可以直接放入列表中
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{fetchTool},
			},
		},

		// MaxIterations: ReAct 循环的最大迭代次数
		// 网页抓取任务通常比较简单，10 次迭代足够
		MaxIterations: 10,
	})
}
