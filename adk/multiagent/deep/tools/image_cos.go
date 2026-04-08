package tools

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/google/uuid"
	"github.com/tencentyun/cos-go-sdk-v5"
)

// ImageCOSManager 图片 COS 文件管理器
type ImageCOSManager struct {
	client    *cos.Client
	bucket    string
	region    string
	prefix    string // 文件前缀路径，如 "image-agent"
	secretId  string
	secretKey string
}

// NewImageCOSManager 创建图片 COS 管理器
// 环境变量：
//   - IMAGE_COS_SECRET_ID:  腾讯云 SecretId
//   - IMAGE_COS_SECRET_KEY: 腾讯云 SecretKey
//   - IMAGE_COS_BUCKET:     COS 存储桶名称
//   - IMAGE_COS_REGION:     COS 区域（如 ap-guangzhou）
//   - IMAGE_COS_PREFIX:     COS 文件前缀路径（默认 image-agent）
func NewImageCOSManager() (*ImageCOSManager, error) {
	secretId := os.Getenv("IMAGE_COS_SECRET_ID")
	secretKey := os.Getenv("IMAGE_COS_SECRET_KEY")
	bucket := os.Getenv("IMAGE_COS_BUCKET")
	region := os.Getenv("IMAGE_COS_REGION")
	prefix := os.Getenv("IMAGE_COS_PREFIX")

	if secretId == "" || secretKey == "" || bucket == "" || region == "" {
		return nil, fmt.Errorf("图片 COS 配置不完整，请设置环境变量: IMAGE_COS_SECRET_ID, IMAGE_COS_SECRET_KEY, IMAGE_COS_BUCKET, IMAGE_COS_REGION")
	}

	if prefix == "" {
		prefix = "image-agent"
	}

	// 构建 COS Bucket URL
	bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region))
	if err != nil {
		return nil, fmt.Errorf("解析 COS Bucket URL 失败: %w", err)
	}

	// 构建 COS Service URL
	serviceURL, err := url.Parse(fmt.Sprintf("https://cos.%s.myqcloud.com", region))
	if err != nil {
		return nil, fmt.Errorf("解析 COS Service URL 失败: %w", err)
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

	return &ImageCOSManager{
		client:    client,
		bucket:    bucket,
		region:    region,
		prefix:    prefix,
		secretId:  secretId,
		secretKey: secretKey,
	}, nil
}

// Upload 上传图片数据到 COS，返回预签名下载 URL（适用于私有桶）
// ext 为文件扩展名，如 ".png"、".jpg"
// 预签名 URL 默认有效期为 7 天
func (m *ImageCOSManager) Upload(data []byte, ext string) (string, error) {
	// 生成唯一文件名
	filename := fmt.Sprintf("%s%s", uuid.New().String(), ext)
	key := path.Join(m.prefix, time.Now().Format("2006-01-02"), filename)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 检测 Content-Type
	contentType := http.DetectContentType(data)

	opt := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType: contentType,
		},
	}

	_, err := m.client.Object.Put(ctx, key, bytes.NewReader(data), opt)
	if err != nil {
		return "", fmt.Errorf("上传图片到 COS 失败 (key=%s): %w", key, err)
	}

	// 生成预签名下载 URL（7 天有效期）
	presignedURL, err := m.GetPresignedURL(key, 7*24*time.Hour)
	if err != nil {
		// 如果预签名失败，回退到公开链接
		cdnURL := fmt.Sprintf("https://%s.cos.%s.myqcloud.com/%s", m.bucket, m.region, key)
		fmt.Printf("[ImageCOSManager] 预签名失败，回退到公开链接: %s (%d bytes) -> %s\n", key, len(data), cdnURL)
		return cdnURL, nil
	}

	fmt.Printf("[ImageCOSManager] 图片已上传: %s (%d bytes) -> 预签名URL已生成\n", key, len(data))
	return presignedURL, nil
}

// GetPresignedURL 生成预签名下载 URL（用于私有桶）
func (m *ImageCOSManager) GetPresignedURL(key string, expiry time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	presignedURL, err := m.client.Object.GetPresignedURL(ctx, http.MethodGet, key, m.secretId, m.secretKey, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("生成预签名 URL 失败 (key=%s): %w", key, err)
	}

	rawURL := fmt.Sprintf("%s://%s%s?%s", presignedURL.Scheme, presignedURL.Host, presignedURL.Path, presignedURL.RawQuery)
	return rawURL, nil
}
