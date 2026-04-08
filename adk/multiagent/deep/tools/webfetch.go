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
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// webfetchToolInfo 定义了 webfetch 工具的元信息，供 LLM 理解该工具的用途和参数
var webfetchToolInfo = &schema.ToolInfo{
	Name: "webfetch",
	Desc: `Fetch the content of a web page by URL and return the raw text content.
* This tool is useful for retrieving information from a specific web page.
* It supports HTTP and HTTPS URLs.
* The response content will be truncated if it exceeds the maximum length.
* For HTML pages, the raw HTML will be returned. You may need to parse it to extract useful information.
* Timeout is 30 seconds by default.`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"url": {
			Type:     schema.String,
			Desc:     "The URL of the web page to fetch, must start with http:// or https://",
			Required: true,
		},
		"max_length": {
			Type: schema.Integer,
			Desc: "Maximum length of the response content in characters. Default is 50000. Set to -1 for no limit.",
		},
	}),
}

// NewWebFetchTool 创建一个 webfetch 工具实例
func NewWebFetchTool() tool.InvokableTool {
	return &webFetchTool{}
}

// webFetchTool 实现了 tool.InvokableTool 接口
type webFetchTool struct{}

// Info 返回工具的元信息，LLM 通过这个方法了解工具的名称、描述和参数定义
func (w *webFetchTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return webfetchToolInfo, nil
}

// webFetchInput 定义了工具的输入参数结构体，用于反序列化 LLM 传入的 JSON 参数
type webFetchInput struct {
	URL       string `json:"url"`
	MaxLength int    `json:"max_length"`
}

// InvokableRun 执行网页抓取逻辑
// argumentsInJSON 是 LLM 生成的 JSON 格式参数字符串
// 返回值是网页内容的文本字符串
func (w *webFetchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 1. 解析 JSON 参数
	input := &webFetchInput{}
	err := json.Unmarshal([]byte(argumentsInJSON), input)
	if err != nil {
		return "", err
	}

	// 2. 校验参数
	if input.URL == "" {
		return "url cannot be empty", nil
	}
	if !strings.HasPrefix(input.URL, "http://") && !strings.HasPrefix(input.URL, "https://") {
		return "url must start with http:// or https://", nil
	}
	if input.MaxLength == 0 {
		input.MaxLength = 50000
	}

	// 3. 创建带超时的 HTTP 客户端并发送请求
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// 设置常见的浏览器 User-Agent，避免被网站拒绝
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("failed to fetch url: %v", err), nil
	}
	defer resp.Body.Close()

	// 4. 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("unexpected status code: %d", resp.StatusCode), nil
	}

	// 5. 读取响应内容
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("failed to read response body: %v", err), nil
	}

	content := string(body)

	// 6. 截断超长内容
	if input.MaxLength > 0 && len(content) > input.MaxLength {
		content = content[:input.MaxLength] + "\n\n... [content truncated]"
	}

	return content, nil
}
