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

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// 允许编辑的 agent-core 文件白名单
var allowedMemoryFiles = map[string]bool{
	"SOUL.md":   true,
	"AGENTS.md": true,
	"TOOLS.md":  true,
	"MEMORY.md": true,
}

var memoryEditToolInfo = &schema.ToolInfo{
	Name: "memory_edit",
	Desc: `编辑 agent-core 目录下的长期记忆文件，用于将已确认的学习提升为永久规则。
仅支持编辑以下文件（全量覆盖）：
- SOUL.md：行为准则、沟通风格、用户偏好
- AGENTS.md：Agent 工作流、工具使用模式、自动化规则
- TOOLS.md：工具能力、使用模式、集成注意事项
- MEMORY.md：项目事实、约定、长期知识

注意：content 参数应包含文件的完整内容（全量覆盖），请先用 read_file 读取当前内容再修改。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"file": {
			Type:     schema.String,
			Desc:     "目标文件名（SOUL.md / AGENTS.md / TOOLS.md / MEMORY.md）",
			Required: true,
		},
		"content": {
			Type:     schema.String,
			Desc:     "文件的完整新内容（全量覆盖）",
			Required: true,
		},
	}),
}

// NewMemoryEditTool 创建 agent-core 记忆文件编辑工具
// agentCoreDir 是 agent-core 目录的绝对路径
func NewMemoryEditTool(agentCoreDir string) tool.InvokableTool {
	return &memoryEdit{agentCoreDir: agentCoreDir}
}

type memoryEdit struct {
	agentCoreDir string
}

func (m *memoryEdit) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return memoryEditToolInfo, nil
}

type memoryEditInput struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

func (m *memoryEdit) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	input := &memoryEditInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", err
	}
	if input.File == "" {
		return "file can not be empty", nil
	}
	if input.Content == "" {
		return "content can not be empty", nil
	}

	// 安全检查：只允许编辑白名单中的文件
	if !allowedMemoryFiles[input.File] {
		return fmt.Sprintf("file '%s' is not allowed, only SOUL.md / AGENTS.md / TOOLS.md / MEMORY.md are supported", input.File), nil
	}

	filePath := filepath.Join(m.agentCoreDir, input.File)
	if err := os.WriteFile(filePath, []byte(input.Content), 0o666); err != nil {
		return fmt.Sprintf("failed to write %s: %v", input.File, err), nil
	}

	return fmt.Sprintf("memory file %s updated successfully", input.File), nil
}
