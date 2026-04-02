package sandbox

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 全局配置结构体
type Config struct {
	Tencent TencentConfig `yaml:"tencent"`
	Sandbox SandboxConfig `yaml:"sandbox"`
}

// TencentConfig 腾讯云配置
type TencentConfig struct {
	AGS AGSConfig `yaml:"ags"`
	COS COSConfig `yaml:"cos"`
}

// AGSConfig 腾讯云AGS配置
type AGSConfig struct {
	SecretId  string `yaml:"secret_id"`
	SecretKey string `yaml:"secret_key"`
	Region    string `yaml:"region"`
}

// COSConfig 腾讯云COS配置
type COSConfig struct {
	Bucket string `yaml:"bucket"` // COS存储桶名称（含AppId），如 "test-1300106390"
	Region string `yaml:"region"` // COS区域，如 "ap-guangzhou"
	Prefix string `yaml:"prefix"` // 文件前缀路径，默认 "sandbox-tasks"
}

// SandboxConfig 沙箱配置
type SandboxConfig struct {
	ToolId string `yaml:"tool_id"` // 沙箱工具ID（用于创建临时实例）
}

// LoadConfig 从指定路径加载配置文件
// 配置优先级：环境变量 > 配置文件中的值 > 默认值
// 支持的环境变量：
//   - SANDBOX_SECRET_ID:  腾讯云 SecretId
//   - SANDBOX_SECRET_KEY: 腾讯云 SecretKey
//   - SANDBOX_REGION:     腾讯云区域（如 ap-guangzhou）
//   - SANDBOX_COS_BUCKET: COS 存储桶名称
//   - SANDBOX_COS_REGION: COS 区域（默认与 AGS 区域一致）
//   - SANDBOX_COS_PREFIX: COS 文件前缀路径（默认 sandbox-tasks）
//   - SANDBOX_TOOL_ID:    沙箱工具ID
func LoadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 环境变量覆盖：如果设置了对应的环境变量，则优先使用环境变量的值
	// os.Getenv 会返回环境变量的值，如果环境变量不存在则返回空字符串 ""
	if v := os.Getenv("SANDBOX_SECRET_ID"); v != "" {
		config.Tencent.AGS.SecretId = v
	}
	if v := os.Getenv("SANDBOX_SECRET_KEY"); v != "" {
		config.Tencent.AGS.SecretKey = v
	}
	if v := os.Getenv("SANDBOX_REGION"); v != "" {
		config.Tencent.AGS.Region = v
	}
	if v := os.Getenv("SANDBOX_COS_BUCKET"); v != "" {
		config.Tencent.COS.Bucket = v
	}
	if v := os.Getenv("SANDBOX_COS_REGION"); v != "" {
		config.Tencent.COS.Region = v
	}
	if v := os.Getenv("SANDBOX_COS_PREFIX"); v != "" {
		config.Tencent.COS.Prefix = v
	}
	if v := os.Getenv("SANDBOX_TOOL_ID"); v != "" {
		config.Sandbox.ToolId = v
	}

	// 设置COS默认前缀
	if config.Tencent.COS.Prefix == "" {
		config.Tencent.COS.Prefix = "sandbox-tasks"
	}

	// COS区域默认与AGS区域一致
	if config.Tencent.COS.Region == "" {
		config.Tencent.COS.Region = config.Tencent.AGS.Region
	}

	return &config, nil
}
