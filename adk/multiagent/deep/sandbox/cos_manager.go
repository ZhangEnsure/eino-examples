package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// COSManager COS文件管理器
type COSManager struct {
	client    *cos.Client
	bucket    string
	region    string
	prefix    string // 文件前缀路径，如 "sandbox-tasks"
	secretId  string // 保存密钥ID，用于生成预签名URL
	secretKey string // 保存密钥Key，用于生成预签名URL
}

// NewCOSManager 创建COS管理器
func NewCOSManager(secretId, secretKey, bucket, region, prefix string) (*COSManager, error) {
	// 构建COS Bucket URL
	bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region))
	if err != nil {
		return nil, fmt.Errorf("解析COS Bucket URL失败: %w", err)
	}

	// 构建COS Service URL
	serviceURL, err := url.Parse(fmt.Sprintf("https://cos.%s.myqcloud.com", region))
	if err != nil {
		return nil, fmt.Errorf("解析COS Service URL失败: %w", err)
	}

	baseURL := &cos.BaseURL{
		BucketURL:  bucketURL,
		ServiceURL: serviceURL,
	}

	client := cos.NewClient(baseURL, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretId,
			SecretKey: secretKey,
		},
	})

	if prefix == "" {
		prefix = "sandbox-tasks"
	}

	return &COSManager{
		client:    client,
		bucket:    bucket,
		region:    region,
		prefix:    prefix,
		secretId:  secretId,
		secretKey: secretKey,
	}, nil
}

// taskInputKey 生成任务输入文件的COS Key
func (m *COSManager) taskInputKey(taskId, filename string) string {
	return path.Join(m.prefix, taskId, "input", filename)
}

// taskOutputKey 生成任务输出文件的COS Key
func (m *COSManager) taskOutputKey(taskId, filename string) string {
	return path.Join(m.prefix, taskId, "output", filename)
}

// taskScriptKey 生成任务脚本文件的COS Key
func (m *COSManager) taskScriptKey(taskId, filename string) string {
	return path.Join(m.prefix, taskId, "script", filename)
}

// SandboxInputPath 返回沙箱内的输入文件路径（/data/cos/prefix/taskId/input/filename）
func (m *COSManager) SandboxInputPath(taskId, filename string) string {
	return path.Join("/data/cos", m.taskInputKey(taskId, filename))
}

// SandboxOutputPath 返回沙箱内的输出文件路径
func (m *COSManager) SandboxOutputPath(taskId, filename string) string {
	return path.Join("/data/cos", m.taskOutputKey(taskId, filename))
}

// SandboxScriptPath 返回沙箱内的脚本文件路径
func (m *COSManager) SandboxScriptPath(taskId, filename string) string {
	return path.Join("/data/cos", m.taskScriptKey(taskId, filename))
}

// SandboxWorkDir 返回沙箱内的任务工作目录
func (m *COSManager) SandboxWorkDir(taskId string) string {
	return path.Join("/data/cos", m.prefix, taskId)
}

// Upload 上传文件到COS
func (m *COSManager) Upload(taskId, filename string, data []byte, fileType string) (string, error) {
	var key string
	switch fileType {
	case "input":
		key = m.taskInputKey(taskId, filename)
	case "output":
		key = m.taskOutputKey(taskId, filename)
	case "script":
		key = m.taskScriptKey(taskId, filename)
	default:
		key = m.taskInputKey(taskId, filename)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := m.client.Object.Put(ctx, key, bytes.NewReader(data), nil)
	if err != nil {
		return "", fmt.Errorf("上传文件到COS失败 (key=%s): %w", key, err)
	}

	fmt.Printf("[COSManager] 文件已上传: %s (%d bytes)\n", key, len(data))
	return key, nil
}

// Download 从COS下载文件
func (m *COSManager) Download(taskId, filename string, fileType string) ([]byte, error) {
	var key string
	switch fileType {
	case "input":
		key = m.taskInputKey(taskId, filename)
	case "output":
		key = m.taskOutputKey(taskId, filename)
	case "script":
		key = m.taskScriptKey(taskId, filename)
	default:
		key = m.taskOutputKey(taskId, filename)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := m.client.Object.Get(ctx, key, nil)
	if err != nil {
		return nil, fmt.Errorf("从COS下载文件失败 (key=%s): %w", key, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取COS文件内容失败 (key=%s): %w", key, err)
	}

	fmt.Printf("[COSManager] 文件已下载: %s (%d bytes)\n", key, len(data))
	return data, nil
}

// CleanTask 清理任务相关的所有COS文件
func (m *COSManager) CleanTask(taskId string) error {
	prefix := path.Join(m.prefix, taskId) + "/"

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	// 列出所有匹配前缀的对象
	opt := &cos.BucketGetOptions{
		Prefix:  prefix,
		MaxKeys: 1000,
	}

	result, _, err := m.client.Bucket.Get(ctx, opt)
	if err != nil {
		return fmt.Errorf("列出COS对象失败 (prefix=%s): %w", prefix, err)
	}

	if len(result.Contents) == 0 {
		fmt.Printf("[COSManager] 无需清理: %s (无文件)\n", prefix)
		return nil
	}

	// 批量删除
	objects := make([]cos.Object, 0, len(result.Contents))
	for _, obj := range result.Contents {
		objects = append(objects, cos.Object{Key: obj.Key})
	}

	delOpt := &cos.ObjectDeleteMultiOptions{
		Objects: objects,
		Quiet:   true,
	}

	_, _, err = m.client.Object.DeleteMulti(ctx, delOpt)
	if err != nil {
		return fmt.Errorf("批量删除COS对象失败: %w", err)
	}

	fmt.Printf("[COSManager] 已清理任务文件: %s (%d个文件)\n", prefix, len(objects))
	return nil
}

// GetPresignedURL 生成预签名下载URL
func (m *COSManager) GetPresignedURL(taskId, filename string, fileType string, expiry time.Duration) (string, error) {
	var key string
	switch fileType {
	case "input":
		key = m.taskInputKey(taskId, filename)
	case "output":
		key = m.taskOutputKey(taskId, filename)
	default:
		key = m.taskOutputKey(taskId, filename)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	presignedURL, err := m.client.Object.GetPresignedURL(ctx, http.MethodGet, key, m.secretId, m.secretKey, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("生成预签名URL失败 (key=%s): %w", key, err)
	}

	// 使用 Scheme + Host + Path + RawQuery 构建URL，避免 url.URL.String() 对分号等字符进行二次编码
	// COS预签名URL中 q-sign-time 等参数包含分号(;)，不能被编码为 %3B
	rawURL := fmt.Sprintf("%s://%s%s?%s", presignedURL.Scheme, presignedURL.Host, presignedURL.Path, presignedURL.RawQuery)
	return rawURL, nil
}

// GetFileSize 获取COS上文件的大小（字节），如果文件不存在返回 -1
func (m *COSManager) GetFileSize(taskId, filename string, fileType string) (int64, error) {
	var key string
	switch fileType {
	case "output":
		key = m.taskOutputKey(taskId, filename)
	case "input":
		key = m.taskInputKey(taskId, filename)
	case "script":
		key = m.taskScriptKey(taskId, filename)
	default:
		key = m.taskOutputKey(taskId, filename)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := m.client.Object.Head(ctx, key, nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "NoSuchKey") {
			return -1, nil
		}
		return -1, fmt.Errorf("获取COS文件信息失败 (key=%s): %w", key, err)
	}

	return resp.ContentLength, nil
}

// CheckFileExists 检查COS上的文件是否存在
func (m *COSManager) CheckFileExists(taskId, filename string, fileType string) (bool, error) {
	var key string
	switch fileType {
	case "output":
		key = m.taskOutputKey(taskId, filename)
	default:
		key = m.taskOutputKey(taskId, filename)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := m.client.Object.Head(ctx, key, nil)
	if err != nil {
		// 如果是404则文件不存在
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "NoSuchKey") {
			return false, nil
		}
		return false, fmt.Errorf("检查COS文件失败 (key=%s): %w", key, err)
	}

	return true, nil
}

// TestConnection 测试COS连接是否正常
func (m *COSManager) TestConnection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := m.client.Bucket.Get(ctx, &cos.BucketGetOptions{
		MaxKeys: 1,
	})
	if err != nil {
		return fmt.Errorf("COS连接测试失败: %w", err)
	}

	fmt.Printf("[COSManager] COS连接正常: %s (%s)\n", m.bucket, m.region)
	return nil
}
