package sandbox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	ags "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ags/v20250920"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// Product 沙箱预期产出文件
type Product struct {
	Name string `json:"name"`
}

// Attachment 上传到沙箱的附件
type Attachment struct {
	Name    string `json:"name"`
	Content string `json:"content"` // base64编码的文件内容
}

// Archive 归档文件（用于上传文件夹，打包为tar.gz）
type Archive struct {
	Name    string `json:"name"`    // 归档文件名，如 "myproject.tar.gz"
	Content string `json:"content"` // base64编码的tar.gz内容
}

// ProductItem 产出文件项
type ProductItem struct {
	URL  string `json:"url"`  // COS预签名下载链接
	Size int64  `json:"size"` // 文件大小（字节）
}

// ExecuteRequest 执行请求参数
type ExecuteRequest struct {
	// Cmd 执行命令，如 "python3 script.py"
	Cmd string `json:"cmd"`
	// ScriptContent 脚本内容（明文，非base64）
	ScriptContent string `json:"script_content"`
	// ScriptName 脚本文件名，如 "script.py"
	ScriptName string `json:"script_name"`
	// Products 预期产出文件名列表
	Products []string `json:"products"`
	// Attachments 附件列表（base64编码内容）
	Attachments []Attachment `json:"attachments"`
	// Archives 归档文件列表（tar.gz/zip格式，用于上传文件夹）
	Archives []Archive `json:"archives,omitempty"`
}

// ExecuteResult 执行结果
type ExecuteResult struct {
	Success  bool                   `json:"success"`
	TaskId   string                 `json:"task_id"`
	Output   string                 `json:"output"`
	Error    string                 `json:"error,omitempty"`
	Products map[string]ProductItem `json:"products,omitempty"`
}

// SandboxManager 沙箱管理器
type SandboxManager struct {
	SecretId   string
	SecretKey  string
	Region     string
	ToolId     string      // 沙箱工具ID
	cosManager *COSManager // COS文件管理器
}

// SandboxInstance 沙箱实例信息（包含访问凭证）
type SandboxInstance struct {
	InstanceId string
	Token      string
	Url        string
}

type sandboxRespBody struct {
	ReturnCode int    `json:"returncode"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Success    bool   `json:"success"`
}

// NewSandboxManager 根据配置创建沙箱管理器
func NewSandboxManager(cfg *Config) (*SandboxManager, error) {
	cosManager, err := NewCOSManager(
		cfg.Tencent.AGS.SecretId,
		cfg.Tencent.AGS.SecretKey,
		cfg.Tencent.COS.Bucket,
		cfg.Tencent.COS.Region,
		cfg.Tencent.COS.Prefix,
	)
	if err != nil {
		return nil, fmt.Errorf("创建COS管理器失败: %w", err)
	}

	return &SandboxManager{
		SecretId:   cfg.Tencent.AGS.SecretId,
		SecretKey:  cfg.Tencent.AGS.SecretKey,
		Region:     cfg.Tencent.AGS.Region,
		ToolId:     cfg.Sandbox.ToolId,
		cosManager: cosManager,
	}, nil
}

// NewSandboxManagerFromConfigFile 从配置文件创建沙箱管理器（便捷方法）
func NewSandboxManagerFromConfigFile(configPath string) (*SandboxManager, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("加载配置失败: %w", err)
	}
	return NewSandboxManager(cfg)
}

// TestConnection 测试COS连接是否正常
func (m *SandboxManager) TestConnection() error {
	return m.cosManager.TestConnection()
}

// Execute 执行沙箱任务（核心对外接口）
// 创建新沙箱实例 -> 通过COS传递文件 -> 执行脚本 -> 获取产出 -> 销毁实例
func (m *SandboxManager) Execute(req *ExecuteRequest) (*ExecuteResult, error) {
	if req.Cmd == "" || req.ScriptContent == "" || req.ScriptName == "" {
		return nil, fmt.Errorf("缺少必要参数: cmd, script_content, script_name")
	}

	taskId := generateTaskId()

	// 构建内部附件列表
	var attachments []attachment
	for _, a := range req.Attachments {
		attachments = append(attachments, attachment{
			Name:    a.Name,
			Content: a.Content,
		})
	}

	// 构建内部产出列表
	var products []product
	for _, name := range req.Products {
		products = append(products, product{Name: name})
	}

	// 构建内部归档列表
	var archives []archive
	for _, a := range req.Archives {
		archives = append(archives, archive{
			Name:    a.Name,
			Content: a.Content,
		})
	}

	fmt.Printf("[SandboxManager] 执行请求: taskId=%s, cmd=%s, script=%s, 附件数=%d, 归档数=%d, 产出数=%d\n",
		taskId, req.Cmd, req.ScriptName, len(attachments), len(archives), len(products))

	result, err := m.startAndExecute(
		taskId,
		req.Cmd,
		req.ScriptContent,
		req.ScriptName,
		attachments,
		archives,
		products,
	)
	if err != nil {
		return &ExecuteResult{
			Success: false,
			TaskId:  taskId,
			Error:   fmt.Sprintf("沙箱执行失败: %v", err),
		}, err
	}

	execResult := &ExecuteResult{
		Success:  result.Error == "",
		TaskId:   taskId,
		Output:   result.Output,
		Error:    result.Error,
		Products: result.Products,
	}

	// 打印沙箱代码执行结果
	fmt.Printf("\n[SandboxManager] ========== 代码执行结果 (taskId=%s) ==========\n", taskId)
	if execResult.Success {
		fmt.Printf("[SandboxManager] ✅ 执行状态: 成功\n")
	} else {
		fmt.Printf("[SandboxManager] ❌ 执行状态: 失败\n")
	}
	if execResult.Output != "" {
		fmt.Printf("[SandboxManager] 📤 标准输出:\n%s\n", execResult.Output)
	}
	if execResult.Error != "" {
		fmt.Printf("[SandboxManager] 📛 错误信息:\n%s\n", execResult.Error)
	}
	if len(execResult.Products) > 0 {
		fmt.Printf("[SandboxManager] 📁 产出文件:\n")
		for name, item := range execResult.Products {
			fmt.Printf("  - %s (%d bytes): %s\n", name, item.Size, item.URL)
		}
	}
	fmt.Printf("[SandboxManager] ================================================\n\n")

	return execResult, nil
}

// --- 以下为内部实现 ---

// product 内部产出文件（小写不导出）
type product struct {
	Name string `json:"name"`
}

// attachment 内部附件（小写不导出）
type attachment struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// archive 内部归档文件（小写不导出）
type archive struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// sandboxExecuteResult 内部执行结果
type sandboxExecuteResult struct {
	Output   string                 `json:"output"`
	Products map[string]ProductItem `json:"products"`
	Error    string                 `json:"error"`
}

// generateTaskId 生成唯一任务ID
func generateTaskId() string {
	return fmt.Sprintf("%s", time.Now().Format("20060102150405")) + "-" + fmt.Sprintf("%08x", time.Now().UnixNano()%0xFFFFFFFF)
}

// newAgsClient 创建AGS客户端
func (m *SandboxManager) newAgsClient(region string) (*ags.Client, error) {
	credential := common.NewCredential(m.SecretId, m.SecretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "ags.tencentcloudapi.com"
	if region == "" {
		region = m.Region
	}
	return ags.NewClient(credential, region, cpf)
}

// acquireToken 为已有的沙箱实例获取访问Token
func (m *SandboxManager) acquireToken(instanceId string) (*SandboxInstance, error) {
	client, err := m.newAgsClient("")
	if err != nil {
		return nil, fmt.Errorf("创建AGS客户端失败: %w", err)
	}

	tokenReq := ags.NewAcquireSandboxInstanceTokenRequest()
	tokenReq.InstanceId = common.StringPtr(instanceId)
	tokenResp, err := client.AcquireSandboxInstanceToken(tokenReq)
	if err != nil {
		return nil, fmt.Errorf("获取Token失败: %w", err)
	}

	sandboxUrl := fmt.Sprintf("https://8080-%s.ap-guangzhou.tencentags.com/execute", instanceId)
	fmt.Printf("[SandboxManager] 获取Token成功, instanceId=%s, URL=%s\n", instanceId, sandboxUrl)

	return &SandboxInstance{
		InstanceId: instanceId,
		Token:      *tokenResp.Response.Token,
		Url:        sandboxUrl,
	}, nil
}

// startInstance 启动沙箱实例并获取访问Token
func (m *SandboxManager) startInstance() (*SandboxInstance, error) {
	client, err := m.newAgsClient(m.Region)
	if err != nil {
		return nil, fmt.Errorf("创建AGS客户端失败: %w", err)
	}

	startReq := ags.NewStartSandboxInstanceRequest()
	startReq.ToolId = common.StringPtr(m.ToolId)
	startReq.CustomConfiguration = &ags.CustomConfiguration{
		Command: common.StringPtrs([]string{"bash", "-c", "cd /tmp && python3 /app/skills_server.py"}),
	}
	startResp, err := client.StartSandboxInstance(startReq)
	if err != nil {
		return nil, fmt.Errorf("启动实例失败: %w", err)
	}

	instanceId := *startResp.Response.Instance.InstanceId
	fmt.Printf("[SandboxManager] 沙箱实例启动成功, instanceId=%s\n", instanceId)

	return m.acquireToken(instanceId)
}

// stopInstance 停止并销毁沙箱实例
func (m *SandboxManager) stopInstance(instanceId string) error {
	client, err := m.newAgsClient(m.Region)
	if err != nil {
		return fmt.Errorf("创建AGS客户端失败: %w", err)
	}

	stopReq := ags.NewStopSandboxInstanceRequest()
	stopReq.InstanceId = common.StringPtr(instanceId)
	_, err = client.StopSandboxInstance(stopReq)
	if err != nil {
		return fmt.Errorf("停止实例失败: %w", err)
	}

	fmt.Printf("[SandboxManager] 沙箱实例已销毁, instanceId=%s\n", instanceId)
	return nil
}

// startAndExecute 创建新实例 -> 上传文件到COS -> 执行脚本 -> 下载产出 -> 销毁实例
func (m *SandboxManager) startAndExecute(taskId, cmd, scriptContent, scriptName string, attachments []attachment, archives []archive, products []product) (*sandboxExecuteResult, error) {
	// 1. 上传文件到COS
	fmt.Printf("[SandboxManager] 正在上传文件到COS (taskId=%s)...\n", taskId)
	if err := m.uploadFilesToCOS(taskId, scriptContent, scriptName, attachments, archives); err != nil {
		return nil, fmt.Errorf("上传文件到COS失败: %w", err)
	}

	// 2. 创建新实例
	fmt.Printf("[SandboxManager] 正在创建新沙箱实例 (toolId=%s)...\n", m.ToolId)
	instance, err := m.startInstance()
	if err != nil {
		m.cosManager.CleanTask(taskId)
		return nil, fmt.Errorf("创建沙箱实例失败: %w", err)
	}

	// 3. 确保执行完成后销毁实例
	defer func() {
		fmt.Printf("[SandboxManager] 正在销毁沙箱实例 (instanceId=%s)...\n", instance.InstanceId)
		if stopErr := m.stopInstance(instance.InstanceId); stopErr != nil {
			fmt.Printf("[SandboxManager] ⚠️ 销毁实例失败: %v\n", stopErr)
		} else {
			fmt.Printf("[SandboxManager] ✅ 实例已销毁\n")
		}
	}()

	// 4. 执行脚本
	fmt.Printf("[SandboxManager] 正在执行脚本...\n")
	result, err := m.executeViaCOS(instance, taskId, cmd, scriptName, attachments, archives, products)
	if err != nil {
		return nil, fmt.Errorf("沙箱执行失败: %w", err)
	}

	// 5. 下载产出文件
	if len(products) > 0 {
		fmt.Printf("[SandboxManager] 正在从COS下载产出文件...\n")
		m.downloadProducts(taskId, products, result)
	}

	// 6. 清理COS文件（延迟清理）
	go func() {
		cleanDelay := productURLExpiry + 5*time.Minute
		fmt.Printf("COS延迟至 %.0f 分钟后清理\n", cleanDelay.Minutes())
		time.Sleep(cleanDelay)
		//if len(result.Products) > 0 {
		//	cleanDelay := productURLExpiry + 5*time.Minute
		//	fmt.Printf("[SandboxManager] 产出文件已生成URL，COS清理延迟至 %.0f 分钟后\n", cleanDelay.Minutes())
		//	time.Sleep(cleanDelay)
		//} else {
		//	time.Sleep(5 * time.Second)
		//}
		if cleanErr := m.cosManager.CleanTask(taskId); cleanErr != nil {
			fmt.Printf("[SandboxManager] ⚠️ 清理COS文件失败: %v\n", cleanErr)
		}
	}()

	return result, nil
}

// uploadFilesToCOS 将脚本、附件和归档文件上传到COS
func (m *SandboxManager) uploadFilesToCOS(taskId, scriptContent, scriptName string, attachments []attachment, archives []archive) error {
	if _, err := m.cosManager.Upload(taskId, scriptName, []byte(scriptContent), "script"); err != nil {
		return fmt.Errorf("上传脚本文件失败: %w", err)
	}

	for _, att := range attachments {
		data, err := base64.StdEncoding.DecodeString(att.Content)
		if err != nil {
			return fmt.Errorf("解码附件 %s 失败: %w", att.Name, err)
		}
		if _, err := m.cosManager.Upload(taskId, att.Name, data, "input"); err != nil {
			return fmt.Errorf("上传附件 %s 失败: %w", att.Name, err)
		}
	}

	// 上传归档文件
	for _, arc := range archives {
		data, err := base64.StdEncoding.DecodeString(arc.Content)
		if err != nil {
			return fmt.Errorf("解码归档文件 %s 失败: %w", arc.Name, err)
		}
		if _, err := m.cosManager.Upload(taskId, arc.Name, data, "input"); err != nil {
			return fmt.Errorf("上传归档文件 %s 失败: %w", arc.Name, err)
		}
	}

	return nil
}

// executeViaCOS 通过COS路径在沙箱中执行脚本
func (m *SandboxManager) executeViaCOS(instance *SandboxInstance, taskId, cmd, scriptName string, attachments []attachment, archives []archive, products []product) (*sandboxExecuteResult, error) {
	var lines []string
	lines = append(lines, "import subprocess,os,sys,shutil,time,tarfile,zipfile")
	lines = append(lines, "os.chdir('/tmp')")

	scriptCOSPath := m.cosManager.SandboxScriptPath(taskId, scriptName)
	lines = append(lines, fmt.Sprintf("for i in range(30):"))
	lines = append(lines, fmt.Sprintf("    if os.path.exists(%q): break", scriptCOSPath))
	lines = append(lines, fmt.Sprintf("    time.sleep(1)"))
	lines = append(lines, fmt.Sprintf("else: print('ERROR: 等待COS文件超时',file=sys.stderr); sys.exit(1)"))

	lines = append(lines, fmt.Sprintf("shutil.copy2(%q, '/tmp/%s')", scriptCOSPath, scriptName))

	for _, att := range attachments {
		inputPath := m.cosManager.SandboxInputPath(taskId, att.Name)
		lines = append(lines, fmt.Sprintf("shutil.copy2(%q, '/tmp/%s')", inputPath, att.Name))
	}

	// 复制并解压归档文件（统一解压到/tmp目录）
	for _, arc := range archives {
		inputPath := m.cosManager.SandboxInputPath(taskId, arc.Name)
		lines = append(lines, fmt.Sprintf("shutil.copy2(%q, '/tmp/%s')", inputPath, arc.Name))

		// 根据文件后缀选择解压方式，统一解压到/tmp
		if strings.HasSuffix(arc.Name, ".tar.gz") || strings.HasSuffix(arc.Name, ".tgz") {
			lines = append(lines, fmt.Sprintf("with tarfile.open('/tmp/%s', 'r:gz') as tf: tf.extractall('/tmp')", arc.Name))
		} else if strings.HasSuffix(arc.Name, ".tar") {
			lines = append(lines, fmt.Sprintf("with tarfile.open('/tmp/%s', 'r:') as tf: tf.extractall('/tmp')", arc.Name))
		} else if strings.HasSuffix(arc.Name, ".zip") {
			lines = append(lines, fmt.Sprintf("with zipfile.ZipFile('/tmp/%s', 'r') as zf: zf.extractall('/tmp')", arc.Name))
		} else {
			// 默认尝试tar.gz
			lines = append(lines, fmt.Sprintf("with tarfile.open('/tmp/%s', 'r:gz') as tf: tf.extractall('/tmp')", arc.Name))
		}
		lines = append(lines, fmt.Sprintf("print('📦 归档文件 %s 已解压到 /tmp')", arc.Name))
	}

	outputDir := m.cosManager.SandboxWorkDir(taskId) + "/output"
	lines = append(lines, fmt.Sprintf("os.makedirs(%q, exist_ok=True)", outputDir))

	lines = append(lines, fmt.Sprintf("r=subprocess.run(%q,shell=True,cwd='/tmp',capture_output=True,text=True,timeout=300)", cmd))
	lines = append(lines, "sys.stdout.write(r.stdout)")
	lines = append(lines, "sys.stderr.write(r.stderr)")

	for _, p := range products {
		outputPath := m.cosManager.SandboxOutputPath(taskId, p.Name)
		lines = append(lines, fmt.Sprintf("if os.path.exists('/tmp/%s'): shutil.copy2('/tmp/%s', %q)", p.Name, p.Name, outputPath))
	}

	lines = append(lines, "sys.exit(r.returncode)")

	bootstrapScript := strings.Join(lines, "\n")
	bootstrapB64 := base64.StdEncoding.EncodeToString([]byte(bootstrapScript))

	finalCmd := fmt.Sprintf(
		"python3 -c exec(__import__('base64').b64decode(b'%s').decode())",
		bootstrapB64)

	reqBody := map[string]string{
		"cmd": finalCmd,
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	fmt.Printf("[SandboxManager] 执行脚本: %s, 原始命令: %s, 附件数: %d, 归档数: %d, 预期产出: %d\n",
		scriptName, cmd, len(attachments), len(archives), len(products))

	req, err := http.NewRequest("POST", instance.Url, strings.NewReader(string(jsonBytes)))
	if err != nil {
		return nil, fmt.Errorf("创建HTTP请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Access-Token", instance.Token)

	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求沙箱失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &sandboxExecuteResult{
			Error: fmt.Sprintf("沙箱返回HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	var sandboxResp sandboxRespBody
	if err := json.Unmarshal(body, &sandboxResp); err != nil {
		return &sandboxExecuteResult{Output: string(body)}, nil
	}

	result := &sandboxExecuteResult{
		Output:   sandboxResp.Stdout,
		Products: make(map[string]ProductItem),
	}

	if !sandboxResp.Success {
		result.Error = fmt.Sprintf("沙箱执行失败 (returncode: %d): %s", sandboxResp.ReturnCode, sandboxResp.Stderr)
	} else if sandboxResp.Stderr != "" {
		result.Error = sandboxResp.Stderr
	}

	return result, nil
}

// 产出文件预签名URL的有效期
const productURLExpiry = 30 * time.Minute

// downloadProducts 从COS获取产出文件信息并生成预签名下载URL
func (m *SandboxManager) downloadProducts(taskId string, products []product, result *sandboxExecuteResult) {
	time.Sleep(2 * time.Second)

	for _, p := range products {
		fileSize, err := m.cosManager.GetFileSize(taskId, p.Name, "output")
		if err != nil {
			fmt.Printf("[SandboxManager] ⚠️ 获取产出文件 %s 大小失败: %v\n", p.Name, err)
			continue
		}
		if fileSize < 0 {
			fmt.Printf("[SandboxManager] ⚠️ 产出文件 %s 不存在\n", p.Name)
			continue
		}

		presignedURL, err := m.cosManager.GetPresignedURL(taskId, p.Name, "output", productURLExpiry)
		if err != nil {
			fmt.Printf("[SandboxManager] ⚠️ 生成产出文件 %s 的预签名URL失败: %v\n", p.Name, err)
			continue
		}
		result.Products[p.Name] = ProductItem{
			URL:  presignedURL,
			Size: fileSize,
		}
		fmt.Printf("[SandboxManager] ✅ 产出文件URL已生成: %s (%d bytes, 有效期%.0f分钟)\n", p.Name, fileSize, productURLExpiry.Minutes())
	}
}

// PackDirectory 将本地文件夹打包为tar.gz格式的字节数据
// dirPath: 要打包的文件夹路径（绝对路径或相对路径）
// 返回: tar.gz格式的字节数据，可直接用于Archive.Content（需base64编码）
func PackDirectory(dirPath string) ([]byte, error) {
	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("无法访问目录 %s: %w", dirPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s 不是一个目录", dirPath)
	}

	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzWriter)

	// 获取目录的绝对路径作为基准
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("获取绝对路径失败: %w", err)
	}

	err = filepath.Walk(absDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 计算相对路径
		relPath, err := filepath.Rel(absDir, path)
		if err != nil {
			return fmt.Errorf("计算相对路径失败: %w", err)
		}

		// 跳过根目录自身
		if relPath == "." {
			return nil
		}

		// 创建tar header
		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return fmt.Errorf("创建tar header失败 (%s): %w", relPath, err)
		}
		header.Name = relPath

		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("写入tar header失败 (%s): %w", relPath, err)
		}

		// 如果是文件，写入内容
		if !fi.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("打开文件失败 (%s): %w", relPath, err)
			}
			defer f.Close()

			if _, err := io.Copy(tarWriter, f); err != nil {
				return fmt.Errorf("写入文件内容失败 (%s): %w", relPath, err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("遍历目录失败: %w", err)
	}

	// 关闭tar和gzip writer
	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("关闭tar writer失败: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return nil, fmt.Errorf("关闭gzip writer失败: %w", err)
	}

	fmt.Printf("[PackDirectory] 目录 %s 已打包为tar.gz (%d bytes)\n", dirPath, buf.Len())
	return buf.Bytes(), nil
}
