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

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

var diarySearchToolInfo = &schema.ToolInfo{
	Name: "diary_search",
	Desc: `在 agent-core/diary 目录中搜索关键词，用于查找历史日记条目。
支持在所有日记文件中进行全文搜索，返回匹配的行及其上下文。

用途：
- 检测重复模式（同一纠正是否出现过多次）
- 查找相关的历史学习条目
- 统计待处理条目数量
- 查找特定区域或优先级的条目`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"keyword": {
			Type:     schema.String,
			Desc:     "搜索关键词（支持正则表达式）",
			Required: true,
		},
		"context_lines": {
			Type: schema.Integer,
			Desc: "每个匹配结果显示的上下文行数（默认 2）",
		},
	}),
}

// NewDiarySearchTool 创建日记搜索工具
// agentCoreDir 是 agent-core 目录的绝对路径
func NewDiarySearchTool(agentCoreDir string) tool.InvokableTool {
	return &diarySearch{agentCoreDir: agentCoreDir}
}

type diarySearch struct {
	agentCoreDir string
}

func (d *diarySearch) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return diarySearchToolInfo, nil
}

type diarySearchInput struct {
	Keyword      string `json:"keyword"`
	ContextLines int    `json:"context_lines"`
}

func (d *diarySearch) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	input := &diarySearchInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", err
	}
	if input.Keyword == "" {
		return "keyword can not be empty", nil
	}
	if input.ContextLines <= 0 {
		input.ContextLines = 2
	}

	diaryDir := filepath.Join(d.agentCoreDir, "diary")

	// 使用 grep 进行搜索
	// -r: 递归搜索
	// -n: 显示行号
	// -i: 忽略大小写
	// -C: 显示上下文行
	cmd := exec.CommandContext(ctx, "grep",
		"-rniC", fmt.Sprintf("%d", input.ContextLines),
		input.Keyword,
		diaryDir,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// grep 返回 1 表示没有匹配，不是错误
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return fmt.Sprintf("no matches found for '%s' in diary/", input.Keyword), nil
		}
		// 目录不存在等情况
		if strings.Contains(stderr.String(), "No such file or directory") {
			return "diary directory is empty or does not exist yet", nil
		}
		return fmt.Sprintf("search failed: %v\nstderr: %s", err, stderr.String()), nil
	}

	result := stdout.String()
	if len(result) > 8000 {
		result = result[:8000] + "\n... (output truncated, refine your search keyword)"
	}

	return result, nil
}
