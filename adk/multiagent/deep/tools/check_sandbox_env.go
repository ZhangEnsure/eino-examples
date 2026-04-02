package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"sandbox"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// checkSandboxEnvToolInfo 定义了 check_sandbox_env 工具的元信息
// 这是一个辅助工具，让 LLM 在执行代码前了解沙箱环境的能力
var checkSandboxEnvToolInfo = &schema.ToolInfo{
	Name: "check_sandbox_env",
	Desc: `探测沙箱环境信息，包括已安装的编程语言版本、可用的 Python 包、系统信息等。
在不确定沙箱环境是否支持某个功能时，先调用此工具进行检查。
此工具会在沙箱中执行探测脚本，返回环境详情。`,
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"check_type": {
			Type: schema.String,
			Desc: `检查类型，可选值:
- all: 全部信息（语言版本 + Python包 + 系统信息）
- python_packages: 仅查看已安装的 Python 包列表
- system: 仅查看系统信息（OS、CPU、内存等）
- languages: 仅查看已安装的编程语言版本`,
			Required: true,
		},
	}),
}

// checkSandboxEnvInput 是 LLM 传入的 JSON 参数对应的 Go 结构体
type checkSandboxEnvInput struct {
	CheckType string `json:"check_type"`
}

// checkSandboxEnvTool 实现了 tool.InvokableTool 接口
type checkSandboxEnvTool struct {
	manager *sandbox.SandboxManager
}

// NewCheckSandboxEnvTool 创建一个新的 check_sandbox_env 工具实例
func NewCheckSandboxEnvTool(manager *sandbox.SandboxManager) tool.InvokableTool {
	return &checkSandboxEnvTool{manager: manager}
}

// Info 返回工具的元信息
func (t *checkSandboxEnvTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return checkSandboxEnvToolInfo, nil
}

// InvokableRun 是工具的实际执行逻辑
func (t *checkSandboxEnvTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// 1. 解析 LLM 传入的 JSON 参数
	input := &checkSandboxEnvInput{}
	if err := json.Unmarshal([]byte(argumentsInJSON), input); err != nil {
		return "", fmt.Errorf("解析参数失败: %w", err)
	}

	// 2. 根据检查类型生成对应的探测脚本
	script, err := buildCheckScript(input.CheckType)
	if err != nil {
		return fmt.Sprintf("❌ %v", err), nil
	}

	// 3. 构建沙箱执行请求
	// 探测脚本不需要附件和产出文件，只需要标准输出
	req := &sandbox.ExecuteRequest{
		Cmd:           "python3 check_env.py",
		ScriptContent: script,
		ScriptName:    "check_env.py",
	}

	// 4. 调用沙箱执行
	result, err := t.manager.Execute(req)
	if err != nil {
		return fmt.Sprintf("❌ 环境探测失败: %v", err), nil
	}

	// 5. 格式化返回结果
	return formatCheckEnvResult(result, input.CheckType), nil
}

// buildCheckScript 根据检查类型生成对应的 Python 探测脚本
// 使用 Python 是因为它在沙箱中一定存在，且能方便地获取系统信息
func buildCheckScript(checkType string) (string, error) {
	switch strings.ToLower(checkType) {
	case "all":
		return checkAllScript, nil
	case "python_packages":
		return checkPythonPackagesScript, nil
	case "system":
		return checkSystemScript, nil
	case "languages":
		return checkLanguagesScript, nil
	default:
		return "", fmt.Errorf("不支持的检查类型: %s，可选值: all, python_packages, system, languages", checkType)
	}
}

// formatCheckEnvResult 格式化环境探测结果
func formatCheckEnvResult(result *sandbox.ExecuteResult, checkType string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("🔍 沙箱环境探测结果 (类型: %s)\n", checkType))
	sb.WriteString(strings.Repeat("-", 40) + "\n")

	if result.Output != "" {
		sb.WriteString(result.Output)
		if !strings.HasSuffix(result.Output, "\n") {
			sb.WriteString("\n")
		}
	}

	if result.Error != "" {
		sb.WriteString("\n⚠️ 探测过程中的警告:\n")
		sb.WriteString(result.Error)
		if !strings.HasSuffix(result.Error, "\n") {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// ==================== 探测脚本常量 ====================
// 以下是预定义的 Python 探测脚本，使用 Go 的原始字符串字面量（反引号包裹）
// 反引号内的内容不会进行转义处理，所以可以直接写多行 Python 代码

// checkAllScript 探测全部环境信息
const checkAllScript = `
import subprocess, sys, os, platform

print("=" * 50)
print("📋 沙箱环境完整报告")
print("=" * 50)

# 1. 系统信息
print("\n🖥️ 系统信息:")
print(f"  操作系统: {platform.system()} {platform.release()}")
print(f"  架构: {platform.machine()}")
print(f"  Python 版本: {sys.version}")
print(f"  工作目录: {os.getcwd()}")

# 2. 编程语言版本
print("\n🔧 编程语言版本:")
for cmd, name in [("python3 --version", "Python3"), ("node --version", "Node.js"), ("bash --version | head -1", "Bash")]:
    try:
        r = subprocess.run(cmd, shell=True, capture_output=True, text=True, timeout=10)
        version = r.stdout.strip() or r.stderr.strip()
        print(f"  {name}: {version}")
    except Exception as e:
        print(f"  {name}: 未安装或不可用 ({e})")

# 3. Python 常用包
print("\n📦 Python 已安装的常用包:")
try:
    r = subprocess.run([sys.executable, "-m", "pip", "list", "--format=columns"], capture_output=True, text=True, timeout=30)
    if r.returncode == 0:
        lines = r.stdout.strip().split("\n")
        # 只显示前 50 个包，避免输出过长
        for line in lines[:52]:
            print(f"  {line}")
        if len(lines) > 52:
            print(f"  ... 共 {len(lines)-2} 个包")
    else:
        print(f"  获取包列表失败: {r.stderr}")
except Exception as e:
    print(f"  获取包列表失败: {e}")

# 4. 磁盘空间
print("\n💾 磁盘空间:")
try:
    r = subprocess.run("df -h /tmp", shell=True, capture_output=True, text=True, timeout=10)
    print(f"  {r.stdout.strip()}")
except Exception:
    print("  无法获取磁盘信息")

print("\n" + "=" * 50)
print("✅ 环境探测完成")
`

// checkPythonPackagesScript 仅探测 Python 包
const checkPythonPackagesScript = `
import subprocess, sys

print("📦 Python 已安装的包列表:")
print("-" * 40)
try:
    r = subprocess.run([sys.executable, "-m", "pip", "list", "--format=columns"], capture_output=True, text=True, timeout=30)
    if r.returncode == 0:
        print(r.stdout)
    else:
        print(f"获取包列表失败: {r.stderr}")
except Exception as e:
    print(f"获取包列表失败: {e}")
`

// checkSystemScript 仅探测系统信息
const checkSystemScript = `
import platform, os, sys, subprocess

print("🖥️ 系统信息:")
print("-" * 40)
print(f"操作系统: {platform.system()} {platform.release()}")
print(f"架构: {platform.machine()}")
print(f"处理器: {platform.processor() or '未知'}")
print(f"Python 版本: {sys.version}")
print(f"工作目录: {os.getcwd()}")

print("\n💾 磁盘空间:")
try:
    r = subprocess.run("df -h /tmp", shell=True, capture_output=True, text=True, timeout=10)
    print(r.stdout.strip())
except Exception:
    print("无法获取磁盘信息")

print("\n🧠 内存信息:")
try:
    r = subprocess.run("free -h 2>/dev/null || vm_stat 2>/dev/null || echo '无法获取内存信息'", shell=True, capture_output=True, text=True, timeout=10)
    print(r.stdout.strip())
except Exception:
    print("无法获取内存信息")
`

// checkLanguagesScript 仅探测编程语言版本
const checkLanguagesScript = `
import subprocess

print("🔧 已安装的编程语言版本:")
print("-" * 40)

checks = [
    ("python3 --version", "Python3"),
    ("python --version", "Python"),
    ("node --version", "Node.js"),
    ("npm --version", "npm"),
    ("bash --version | head -1", "Bash"),
    ("gcc --version | head -1", "GCC"),
    ("g++ --version | head -1", "G++"),
    ("java -version 2>&1 | head -1", "Java"),
    ("ruby --version", "Ruby"),
    ("perl --version | head -2 | tail -1", "Perl"),
]

for cmd, name in checks:
    try:
        r = subprocess.run(cmd, shell=True, capture_output=True, text=True, timeout=10)
        version = (r.stdout.strip() or r.stderr.strip())
        if version and r.returncode == 0:
            print(f"✅ {name}: {version}")
        else:
            print(f"❌ {name}: 未安装")
    except Exception:
        print(f"❌ {name}: 未安装")
`
