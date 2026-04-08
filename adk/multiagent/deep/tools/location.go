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
	"net/url"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// 位置工具信息定义
var locationToolInfo = &schema.ToolInfo{
	Name: "location",
	Desc: `查询城市或地点的地理位置信息（基于 OpenStreetMap Nominatim 免费 API）。
* 输入城市或地点名称，返回该地点的真实地理信息，包括经纬度、所属国家/地区、详细地址等。
* 支持中文和英文城市名称，支持全球任意地点查询。
* 返回的是真实的地理编码数据。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"city": {
			Type:     schema.String,
			Desc:     "城市或地点名称，例如：北京、上海、深圳、New York、Tokyo",
			Required: true,
		},
	}),
}

// NewLocationTool 创建一个位置信息查询工具
func NewLocationTool() tool.InvokableTool {
	return &locationTool{}
}

type locationTool struct{}

func (l *locationTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return locationToolInfo, nil
}

// locationInput 位置查询输入参数
type locationInput struct {
	City string `json:"city"`
}

// locationResult 位置查询结果
type locationResult struct {
	City        string  `json:"city"`
	DisplayName string  `json:"display_name"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Country     string  `json:"country,omitempty"`
	State       string  `json:"state,omitempty"`
	Type        string  `json:"type,omitempty"`
	Importance  float64 `json:"importance,omitempty"`
}

// InvokableRun 执行位置查询
func (l *locationTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 1. 解析 JSON 参数
	input := &locationInput{}
	err := json.Unmarshal([]byte(argumentsInJSON), input)
	if err != nil {
		return "", err
	}
	if len(input.City) == 0 {
		return "city 参数不能为空", nil
	}

	// 2. 调用 Nominatim API 查询地理位置
	results, err := searchLocation(input.City)
	if err != nil {
		return fmt.Sprintf("查询地理位置失败: %v", err), nil
	}
	if len(results) == 0 {
		return fmt.Sprintf("未找到该地点的地理信息: %s", input.City), nil
	}

	// 3. 格式化返回（返回最匹配的结果）
	resultJSON, err := json.Marshal(results[0])
	if err != nil {
		return "", err
	}
	return string(resultJSON), nil
}

// searchLocation 使用 Nominatim API 搜索地理位置
func searchLocation(query string) ([]*locationResult, error) {
	apiURL := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=3&addressdetails=1&accept-language=zh-CN,en",
		url.QueryEscape(query),
	)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	// Nominatim 要求设置 User-Agent
	req.Header.Set("User-Agent", "EinoUtilityTool/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求地理编码服务失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 解析 Nominatim 响应
	var nominatimResults []struct {
		Lat         string  `json:"lat"`
		Lon         string  `json:"lon"`
		DisplayName string  `json:"display_name"`
		Type        string  `json:"type"`
		Importance  float64 `json:"importance"`
		Address     struct {
			City        string `json:"city"`
			Town        string `json:"town"`
			Village     string `json:"village"`
			State       string `json:"state"`
			Country     string `json:"country"`
			CountryCode string `json:"country_code"`
		} `json:"address"`
	}

	if err := json.Unmarshal(body, &nominatimResults); err != nil {
		return nil, fmt.Errorf("解析地理编码响应失败: %w", err)
	}

	var results []*locationResult
	for _, r := range nominatimResults {
		var lat, lon float64
		fmt.Sscanf(r.Lat, "%f", &lat)
		fmt.Sscanf(r.Lon, "%f", &lon)

		// 获取城市名（优先级：city > town > village > 原始查询）
		cityName := r.Address.City
		if cityName == "" {
			cityName = r.Address.Town
		}
		if cityName == "" {
			cityName = r.Address.Village
		}
		if cityName == "" {
			cityName = query
		}

		results = append(results, &locationResult{
			City:        cityName,
			DisplayName: r.DisplayName,
			Latitude:    lat,
			Longitude:   lon,
			Country:     r.Address.Country,
			State:       r.Address.State,
			Type:        r.Type,
			Importance:  r.Importance,
		})
	}

	return results, nil
}
