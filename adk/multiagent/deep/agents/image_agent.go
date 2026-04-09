package agents

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/cloudwego/eino-examples/adk/multiagent/deep/tools"
	"github.com/cloudwego/eino-examples/adk/multiagent/deep/utils"
)

// NewImageAgent 创建一个图像处理 Agent
// 该 Agent 使用 ReAct 模式，提供图片生成、图片编辑、图片查看分析等能力。
// 主 Agent 在需要处理图片相关任务时，会自动委派给此 Agent。
//
// 需要的环境变量：
//   - OPENAI_PIC_MODEL:     图片处理模型名称（如 gemini-3.1-flash-image）
//   - OPENAI_API_KEY:       OpenAI API Key
//   - OPENAI_BASE_URL:      OpenAI API Base URL
//   - IMAGE_COS_SECRET_ID:  腾讯云 COS SecretId
//   - IMAGE_COS_SECRET_KEY: 腾讯云 COS SecretKey
//   - IMAGE_COS_BUCKET:     COS 存储桶名称
//   - IMAGE_COS_REGION:     COS 区域（如 ap-guangzhou）
//   - IMAGE_COS_PREFIX:     COS 文件前缀路径（可选，默认 image-agent）
func NewImageAgent(ctx context.Context) (adk.Agent, error) {
	// 创建 LLM 聊天模型，使用默认参数
	cm, err := utils.NewChatModel(ctx)
	if err != nil {
		return nil, err
	}

	// 创建图片 COS 管理器
	cosManager, err := tools.NewImageCOSManager()
	if err != nil {
		return nil, fmt.Errorf("创建图片 COS 管理器失败: %w", err)
	}

	// 创建图片处理工具实例
	imageGenerateTool := tools.NewImageGenerateTool(cosManager)
	imageEditTool := tools.NewImageEditTool(cosManager)
	seeImageTool := tools.NewSeeImageTool()

	// 使用 adk.NewChatModelAgent 创建一个 ReAct 模式的 Agent
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		// Name: Agent 的唯一标识名称，主 Agent 通过这个名称来调度子 Agent
		Name: "ImageAgent",

		// Description: Agent 的能力描述，主 Agent 根据这个描述来决定何时调用此子 Agent
		Description: `ImageAgent provides image processing capabilities including generation, editing, and analysis.
It can:
- Generate images from text descriptions (returns CDN URL) - use image_generate tool
- Edit images with various operations like background removal, object replacement, color adjustment, style transfer, etc. (returns CDN URL) - use image_edit tool
- View and analyze local or web images, including content description, OCR text recognition, object detection, scene analysis, etc. - use see_image tool
Use this agent when you need to generate, edit, or analyze images.`,

		// Model: 使用的 LLM 模型（用于 Agent 的推理和决策，不是图片处理模型）
		Model: cm,

		// ToolsConfig: 配置此 Agent 可以使用的工具
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{imageGenerateTool, imageEditTool, seeImageTool},
			},
		},

		// MaxIterations: ReAct 循环的最大迭代次数
		// 图片处理可能需要多步操作，15 次迭代足够
		MaxIterations: 15,
	})
}
