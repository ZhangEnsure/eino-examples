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

// 天气工具信息定义
var weatherToolInfo = &schema.ToolInfo{
	Name: "weather",
	Desc: `查询指定城市的实时天气信息（基于 Open-Meteo 免费 API）。
* 输入城市名称，返回该城市当前的天气状况，包括温度、湿度、天气描述和风速等信息。
* 支持中文和英文城市名称。
* 返回的是真实的实时天气数据。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"city": {
			Type:     schema.String,
			Desc:     "城市名称，例如：北京、上海、深圳、Beijing、Shanghai",
			Required: true,
		},
	}),
}

// NewWeatherTool 创建一个天气查询工具
func NewWeatherTool() tool.InvokableTool {
	return &weatherTool{}
}

type weatherTool struct{}

func (w *weatherTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return weatherToolInfo, nil
}

// weatherInput 天气查询输入参数
type weatherInput struct {
	City string `json:"city"`
}

// weatherResult 天气查询结果
type weatherResult struct {
	City        string  `json:"city"`
	Weather     string  `json:"weather"`
	Temperature float64 `json:"temperature_celsius"`
	FeelsLike   float64 `json:"feels_like_celsius"`
	Humidity    int     `json:"humidity_percent"`
	WindSpeed   float64 `json:"wind_speed_kmh"`
	WindDir     float64 `json:"wind_direction_deg"`
	Pressure    float64 `json:"pressure_hpa"`
	Visibility  float64 `json:"visibility_km,omitempty"`
	UVIndex     float64 `json:"uv_index,omitempty"`
	QueryTime   string  `json:"query_time"`
}

// WMO 天气代码映射为中文描述
var wmoWeatherCodes = map[int]string{
	0:  "晴天",
	1:  "大部晴朗",
	2:  "局部多云",
	3:  "阴天",
	45: "雾",
	48: "沉积雾凇",
	51: "小毛毛雨",
	53: "中毛毛雨",
	55: "大毛毛雨",
	56: "冻毛毛雨（小）",
	57: "冻毛毛雨（大）",
	61: "小雨",
	63: "中雨",
	65: "大雨",
	66: "冻雨（小）",
	67: "冻雨（大）",
	71: "小雪",
	73: "中雪",
	75: "大雪",
	77: "雪粒",
	80: "阵雨（小）",
	81: "阵雨（中）",
	82: "阵雨（大）",
	85: "阵雪（小）",
	86: "阵雪（大）",
	95: "雷暴",
	96: "雷暴伴小冰雹",
	99: "雷暴伴大冰雹",
}

// InvokableRun 执行天气查询
func (w *weatherTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 1. 解析 JSON 参数
	input := &weatherInput{}
	err := json.Unmarshal([]byte(argumentsInJSON), input)
	if err != nil {
		return "", err
	}
	if len(input.City) == 0 {
		return "city 参数不能为空", nil
	}

	// 2. 通过 Nominatim（OpenStreetMap）获取城市经纬度
	lat, lon, displayName, err := geocodeCity(input.City)
	if err != nil {
		return fmt.Sprintf("无法获取城市 '%s' 的地理坐标: %v", input.City, err), nil
	}

	// 3. 调用 Open-Meteo API 获取实时天气
	result, err := fetchWeather(lat, lon, input.City, displayName)
	if err != nil {
		return fmt.Sprintf("获取天气数据失败: %v", err), nil
	}

	// 4. 格式化返回
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(resultJSON), nil
}

// geocodeCity 使用 Nominatim API 将城市名转换为经纬度
func geocodeCity(city string) (lat, lon float64, displayName string, err error) {
	apiURL := fmt.Sprintf("https://nominatim.openstreetmap.org/search?q=%s&format=json&limit=1",
		url.QueryEscape(city))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return 0, 0, "", err
	}
	// Nominatim 要求设置 User-Agent
	req.Header.Set("User-Agent", "EinoUtilityTool/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, "", fmt.Errorf("请求地理编码服务失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, "", fmt.Errorf("读取响应失败: %w", err)
	}

	var results []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return 0, 0, "", fmt.Errorf("解析地理编码响应失败: %w", err)
	}
	if len(results) == 0 {
		return 0, 0, "", fmt.Errorf("未找到城市: %s", city)
	}

	// 解析经纬度
	fmt.Sscanf(results[0].Lat, "%f", &lat)
	fmt.Sscanf(results[0].Lon, "%f", &lon)
	displayName = results[0].DisplayName

	return lat, lon, displayName, nil
}

// fetchWeather 调用 Open-Meteo API 获取实时天气数据
func fetchWeather(lat, lon float64, city, displayName string) (*weatherResult, error) {
	apiURL := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,apparent_temperature,weather_code,wind_speed_10m,wind_direction_10m,surface_pressure&timezone=auto",
		lat, lon,
	)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("请求天气服务失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取天气响应失败: %w", err)
	}

	// 解析 Open-Meteo 响应
	var meteoResp struct {
		Current struct {
			Time               string  `json:"time"`
			Temperature2m      float64 `json:"temperature_2m"`
			RelativeHumidity2m int     `json:"relative_humidity_2m"`
			ApparentTemp       float64 `json:"apparent_temperature"`
			WeatherCode        int     `json:"weather_code"`
			WindSpeed10m       float64 `json:"wind_speed_10m"`
			WindDirection10m   float64 `json:"wind_direction_10m"`
			SurfacePressure    float64 `json:"surface_pressure"`
		} `json:"current"`
	}

	if err := json.Unmarshal(body, &meteoResp); err != nil {
		return nil, fmt.Errorf("解析天气数据失败: %w", err)
	}

	// 将 WMO 天气代码转换为中文描述
	weatherDesc := "未知"
	if desc, ok := wmoWeatherCodes[meteoResp.Current.WeatherCode]; ok {
		weatherDesc = desc
	}

	return &weatherResult{
		City:        city,
		Weather:     weatherDesc,
		Temperature: meteoResp.Current.Temperature2m,
		FeelsLike:   meteoResp.Current.ApparentTemp,
		Humidity:    meteoResp.Current.RelativeHumidity2m,
		WindSpeed:   meteoResp.Current.WindSpeed10m,
		WindDir:     meteoResp.Current.WindDirection10m,
		Pressure:    meteoResp.Current.SurfacePressure,
		QueryTime:   meteoResp.Current.Time,
	}, nil
}
