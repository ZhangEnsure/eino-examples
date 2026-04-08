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
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/eino/adk"
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
	query := schema.UserMessage("请帮我总结知识内容：https://golang-china.github.io/gopl-zh/ch8/ch8-01.html")
	//query := schema.UserMessage("请帮我在网上搜索并总结昆明4月旅游注意事项")
	//query := schema.UserMessage("请在沙箱中给出一个快速排序的算法，并给出一个测试用例的排序结果")
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
	// 始终包含 CodeAgent、WebSearchAgent 和 WebFetchAgent，如果沙箱可用则额外添加 SandboxCodeAgent
	subAgents := []adk.Agent{ca, wa, wfa}
	if sca != nil {
		subAgents = append(subAgents, sca)
	}

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
	})
	if err != nil {
		return nil, err
	}

	return deepAgent, nil
}
