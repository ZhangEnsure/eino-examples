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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

var diaryWriteToolInfo = &schema.ToolInfo{
	Name: "diary_write",
	Desc: `向 agent-core 的每日日记文件追加条目（append-only，不会覆盖已有内容）。
日记文件路径为 agent-core/diary/YYYY-MM-DD.md，以当天日期自动命名。
如果文件不存在则自动创建（含日期标题头）。

用途：
- 记录学习条目 [LRN]：用户纠正、知识差距、最佳实践
- 记录错误条目 [ERR]：命令失败、异常、意外行为
- 记录功能请求 [FEAT]：用户需要但尚不具备的能力
- 记录自我反思 [REF]：完成重要工作后的质量评估

注意：content 参数应包含完整的 Markdown 格式条目（含 ### 标题行）。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"content": {
			Type:     schema.String,
			Desc:     "要追加到日记的条目内容（Markdown 格式，应包含 ### 标题行和所有字段）",
			Required: true,
		},
	}),
}

// NewDiaryWriteTool 创建日记写入工具
// agentCoreDir 是 agent-core 目录的绝对路径
func NewDiaryWriteTool(agentCoreDir string) tool.InvokableTool {
	return &diaryWrite{agentCoreDir: agentCoreDir}
}

type diaryWrite struct {
	agentCoreDir string
}

func (d *diaryWrite) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return diaryWriteToolInfo, nil
}

type diaryWriteInput struct {
	Content string `json:"content"`
}

func (d *diaryWrite) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	input := &diaryWriteInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", err
	}
	if input.Content == "" {
		return "content can not be empty", nil
	}

	// 确保 diary 目录存在
	diaryDir := filepath.Join(d.agentCoreDir, "diary")
	if err := os.MkdirAll(diaryDir, 0o755); err != nil {
		return fmt.Sprintf("failed to create diary directory: %v", err), nil
	}

	// 以当天日期命名文件
	today := time.Now().Format("2006-01-02")
	diaryPath := filepath.Join(diaryDir, today+".md")

	// 检查文件是否存在，不存在则创建并写入日期标题
	var needHeader bool
	if _, err := os.Stat(diaryPath); os.IsNotExist(err) {
		needHeader = true
	}

	// 以追加模式打开文件
	f, err := os.OpenFile(diaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return fmt.Sprintf("failed to open diary file: %v", err), nil
	}
	defer f.Close()

	// 如果是新文件，写入日期标题
	if needHeader {
		header := fmt.Sprintf("# Diary — %s\n\n", today)
		if _, err := f.WriteString(header); err != nil {
			return fmt.Sprintf("failed to write diary header: %v", err), nil
		}
	}

	// 追加条目内容（确保前后有空行分隔）
	entry := "\n" + input.Content + "\n"
	if _, err := f.WriteString(entry); err != nil {
		return fmt.Sprintf("failed to append diary entry: %v", err), nil
	}

	return fmt.Sprintf("diary entry appended to %s", diaryPath), nil
}
