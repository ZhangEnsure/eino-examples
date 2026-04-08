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
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// 时间工具信息定义
var timeToolInfo = &schema.ToolInfo{
	Name: "time",
	Desc: `查询当前时间信息（基于 WorldTimeAPI 免费 API）。
* 支持查询不同时区的当前时间，返回真实的网络时间数据。
* 可以指定时区名称（如 Asia/Shanghai、America/New_York、Europe/London 等）。
* 如果不指定时区，默认返回 UTC 时间和北京时间。
* 返回格式化的日期时间信息，包括日期、时间、星期、时区和 Unix 时间戳。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"timezone": {
			Type: schema.String,
			Desc: "时区名称，例如：Asia/Shanghai、America/New_York、Europe/London、Asia/Tokyo。不指定则默认返回 UTC 和北京时间",
		},
	}),
}

// NewTimeTool 创建一个时间查询工具
func NewTimeTool() tool.InvokableTool {
	return &timeTool{}
}

type timeTool struct{}

func (t *timeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return timeToolInfo, nil
}

// timeInput 时间查询输入参数
type timeInput struct {
	Timezone string `json:"timezone"`
}

// timeResult 时间查询结果
type timeResult struct {
	Timezone    string `json:"timezone"`
	DateTime    string `json:"datetime"`
	Date        string `json:"date"`
	Time        string `json:"time"`
	Weekday     string `json:"weekday"`
	UTCOffset   string `json:"utc_offset"`
	Timestamp   int64  `json:"timestamp"`
	Abbreviation string `json:"abbreviation,omitempty"`
	DayOfYear   int    `json:"day_of_year,omitempty"`
	WeekNumber  int    `json:"week_number,omitempty"`
	Source      string `json:"source"`
}

// InvokableRun 执行时间查询
func (t *timeTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 1. 解析 JSON 参数
	input := &timeInput{}
	if argumentsInJSON != "" && argumentsInJSON != "{}" {
		err := json.Unmarshal([]byte(argumentsInJSON), input)
		if err != nil {
			return "", err
		}
	}

	// 2. 如果未指定时区，返回 UTC 和北京时间
	if input.Timezone == "" {
		utcResult := fetchWorldTime("UTC")
		shanghaiResult := fetchWorldTime("Asia/Shanghai")

		results := map[string]interface{}{
			"utc":            utcResult,
			"beijing_time":   shanghaiResult,
			"default_notice": "未指定时区，已返回 UTC 和北京时间",
		}
		resultJSON, err := json.Marshal(results)
		if err != nil {
			return "", err
		}
		return string(resultJSON), nil
	}

	// 3. 查询指定时区
	result := fetchWorldTime(input.Timezone)
	if result == nil {
		return fmt.Sprintf("无法识别的时区: %s，请使用标准时区名称，如 Asia/Shanghai、America/New_York 等", input.Timezone), nil
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(resultJSON), nil
}

// fetchWorldTime 调用 WorldTimeAPI 获取指定时区的时间
func fetchWorldTime(timezone string) *timeResult {
	apiURL := fmt.Sprintf("https://worldtimeapi.org/api/timezone/%s", timezone)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		// API 请求失败，使用本地时间作为备用
		return fallbackLocalTime(timezone)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// API 返回错误，使用本地时间作为备用
		return fallbackLocalTime(timezone)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fallbackLocalTime(timezone)
	}

	// 解析 WorldTimeAPI 响应
	var apiResp struct {
		Timezone     string `json:"timezone"`
		UTCOffset    string `json:"utc_offset"`
		DateTime     string `json:"datetime"`
		DayOfYear    int    `json:"day_of_year"`
		WeekNumber   int    `json:"week_number"`
		UnixTime     int64  `json:"unixtime"`
		Abbreviation string `json:"abbreviation"`
		DayOfWeek    int    `json:"day_of_week"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fallbackLocalTime(timezone)
	}

	// 解析日期时间
	parsedTime, err := time.Parse("2006-01-02T15:04:05.999999-07:00", apiResp.DateTime)
	if err != nil {
		// 尝试另一种格式
		parsedTime, err = time.Parse("2006-01-02T15:04:05.999999Z07:00", apiResp.DateTime)
		if err != nil {
			return fallbackLocalTime(timezone)
		}
	}

	weekdayMap := map[int]string{
		0: "星期日", 1: "星期一", 2: "星期二", 3: "星期三",
		4: "星期四", 5: "星期五", 6: "星期六",
	}

	return &timeResult{
		Timezone:     apiResp.Timezone,
		DateTime:     parsedTime.Format("2006-01-02 15:04:05"),
		Date:         parsedTime.Format("2006-01-02"),
		Time:         parsedTime.Format("15:04:05"),
		Weekday:      weekdayMap[apiResp.DayOfWeek],
		UTCOffset:    apiResp.UTCOffset,
		Timestamp:    apiResp.UnixTime,
		Abbreviation: apiResp.Abbreviation,
		DayOfYear:    apiResp.DayOfYear,
		WeekNumber:   apiResp.WeekNumber,
		Source:       "WorldTimeAPI",
	}
}

// fallbackLocalTime 当 API 不可用时，使用本地 Go time 包作为备用
func fallbackLocalTime(timezone string) *timeResult {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil
	}

	now := time.Now().In(loc)
	_, offset := now.Zone()
	hours := offset / 3600
	minutes := (offset % 3600) / 60
	utcOffsetStr := fmt.Sprintf("%+03d:%02d", hours, minutes)
	if minutes < 0 {
		minutes = -minutes
	}

	weekdayMap := map[time.Weekday]string{
		time.Sunday:    "星期日",
		time.Monday:    "星期一",
		time.Tuesday:   "星期二",
		time.Wednesday: "星期三",
		time.Thursday:  "星期四",
		time.Friday:    "星期五",
		time.Saturday:  "星期六",
	}

	_, weekNum := now.ISOWeek()

	return &timeResult{
		Timezone:   timezone,
		DateTime:   now.Format("2006-01-02 15:04:05"),
		Date:       now.Format("2006-01-02"),
		Time:       now.Format("15:04:05"),
		Weekday:    weekdayMap[now.Weekday()],
		UTCOffset:  utcOffsetStr,
		Timestamp:  now.Unix(),
		DayOfYear:  now.YearDay(),
		WeekNumber: weekNum,
		Source:     "local_fallback",
	}
}
