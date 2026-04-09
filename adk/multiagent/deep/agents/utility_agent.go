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

// NewUtilityAgent 创建一个实用工具 Agent
// 该 Agent 使用 ReAct 模式，提供天气查询、时间查询、地理位置查询等实用工具能力。
// 主 Agent 在需要获取天气信息、当前时间或城市地理位置时，会自动委派给此 Agent。
func NewUtilityAgent(ctx context.Context) (adk.Agent, error) {
	// 创建 LLM 聊天模型，使用默认参数
	cm, err := utils.NewChatModel(ctx)
	if err != nil {
		return nil, err
	}

	// 创建实用工具实例
	weatherTool := tools.NewWeatherTool()
	timeTool := tools.NewTimeTool()
	locationTool := tools.NewLocationTool()

	// 使用 adk.NewChatModelAgent 创建一个 ReAct 模式的 Agent
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		// Name: Agent 的唯一标识名称，主 Agent 通过这个名称来调度子 Agent
		Name: "UtilityAgent",

		// Description: Agent 的能力描述，主 Agent 根据这个描述来决定何时调用此子 Agent
		Description: `UtilityAgent provides common utility tools for daily information queries.
It can:
- Query weather information for any city (supports Chinese and English city names, with optional date parameter)
- Query current time in different timezones (supports standard timezone names like Asia/Shanghai, America/New_York, etc.)
- Query geographic location information for cities (including latitude, longitude, country, province, timezone, etc.)
Use this agent when you need to get weather forecasts, current time, or city geographic information.`,

		// Model: 使用的 LLM 模型
		Model: cm,

		// ToolsConfig: 配置此 Agent 可以使用的工具
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{weatherTool, timeTool, locationTool},
			},
		},

		// MaxIterations: ReAct 循环的最大迭代次数
		// 实用工具查询通常比较简单，10 次迭代足够
		MaxIterations: 10,
	})
}
