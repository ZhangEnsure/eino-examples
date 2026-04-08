# Eino Framework Import Guide

## Standard Imports for ChatModel Implementation

```go
import (
	"context"
	"errors"
	"io"
	
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)
```

## Core Packages and Types

### `github.com/cloudwego/eino/components/model`

**Interfaces**:
- `BaseChatModel` - Core interface with Generate/Stream methods
- `ChatModel` - **DEPRECATED**, don't use
- `ToolCallingChatModel` - Thread-safe tool support

**Types**:
- `Option` - Call-time option wrapper
- `Options` - Structure holding option values

**Option Constructors**:
```go
model.WithTemperature(0.7)
model.WithMaxTokens(100)
model.WithModel("gpt-4")
model.WithTopP(0.9)
model.WithStop([]string{"\n"})
model.WithTools(tools)
model.WithToolChoice(toolChoice, "tool1", "tool2")
model.WrapImplSpecificOptFn(customOptsFunc)
```

**Helper Functions**:
```go
// Extract common options from Option list
opts := model.GetCommonOptions(&model.Options{}, opts...)

// Extract implementation-specific options
myOpts := model.GetImplSpecificOptions(&MyOptions{}, opts...)
```

---

### `github.com/cloudwego/eino/schema`

**Types**:
- `Message` - Single message in conversation
- `StreamReader[T]` - Generic stream reader for chunks
- `ToolInfo` - Tool definition for model
- `ToolChoice` - Tool choice strategy

**StreamReader Methods**:
```go
reader.Recv() (T, error)           // Get next chunk
reader.Close()                      // Close reader
reader.Copy(n int) []*StreamReader[T]  // Fan-out to N consumers
```

**StreamReader Creation Functions**:
```go
schema.Pipe[T](capacity int)                    // Create channel-based stream
schema.StreamReaderFromArray[T](arr []T)        // Create from slice
schema.MergeStreamReaders[T](readers)           // Merge multiple readers
schema.MergeNamedStreamReaders[T](named map)    // Merge with source tracking
schema.StreamReaderWithConvert[T, D](sr, convert)  // Transform stream values
```

---

### `github.com/cloudwego/eino/components`

**Types**:
- `Component` - String constant type for component category

**Component Constants**:
```go
components.ComponentOfChatModel
components.ComponentOfEmbedding
components.ComponentOfRetriever
components.ComponentOfTool
// ... others
```

**Interfaces** (optional to implement):
- `Typer` - Provide GetType() string
- `Checker` - Provide IsCallbacksEnabled() bool

---

## Complete Example Implementation

```go
package mypkg

import (
	"context"
	"io"
	
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type MyModel struct {
	apiKey string
	tools []*schema.ToolInfo
}

// Implement BaseChatModel
func (m *MyModel) Generate(
	ctx context.Context, 
	input []*schema.Message, 
	opts ...model.Option,
) (*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)
	
	// Use options.Temperature, options.MaxTokens, options.Tools, etc.
	// Perform API call...
	
	return &schema.Message{}, nil
}

func (m *MyModel) Stream(
	ctx context.Context, 
	input []*schema.Message, 
	opts ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	options := model.GetCommonOptions(nil, opts...)
	
	// Use options...
	// Create streaming reader...
	
	return schema.StreamReaderFromArray([]*schema.Message{}), nil
}

// Optionally implement ToolCallingChatModel
func (m *MyModel) WithTools(
	tools []*schema.ToolInfo,
) (model.ToolCallingChatModel, error) {
	return &MyModel{
		apiKey: m.apiKey,
		tools:  tools,
	}, nil
}

// Optionally implement Typer
func (m *MyModel) GetType() string {
	return "MyModel"
}

// Optionally implement Checker
func (m *MyModel) IsCallbacksEnabled() bool {
	return false
}
```

---

## Using the Model

```go
package main

import (
	"context"
	"errors"
	"io"
	
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"mypkg"
)

func main() {
	ctx := context.Background()
	m := mypkg.NewMyModel("api-key")
	
	messages := []*schema.Message{
		{
			Role:    "user",
			Content: "Hello",
		},
	}
	
	// Generate (blocking)
	resp, err := m.Generate(ctx, messages,
		model.WithTemperature(0.7),
		model.WithMaxTokens(100),
	)
	if err != nil {
		panic(err)
	}
	
	// Stream (non-blocking)
	reader, err := m.Stream(ctx, messages,
		model.WithTemperature(0.7),
	)
	if err != nil {
		panic(err)
	}
	defer reader.Close()
	
	for {
		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			panic(err)
		}
		
		// Process chunk
		_ = chunk
	}
	
	// With tools (thread-safe)
	tools := []*schema.ToolInfo{
		// ... tool definitions
	}
	withTools, err := m.WithTools(tools)
	if err != nil {
		panic(err)
	}
	
	resp, err = withTools.Generate(ctx, messages)
	if err != nil {
		panic(err)
	}
}
```

---

## Module Cache Structure

```
~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/
├── components/
│   ├── model/
│   │   ├── interface.go          ← Import: github.com/cloudwego/eino/components/model
│   │   ├── option.go
│   │   ├── ... other files
│   ├── embedding/
│   ├── retriever/
│   ├── tool/
│   ├── types.go                  ← Import: github.com/cloudwego/eino/components
│   └── ... other packages
├── schema/
│   ├── stream.go                 ← Import: github.com/cloudwego/eino/schema
│   ├── message.go
│   ├── tool.go
│   └── ... other files
└── ... other packages
```

---

## Version Information

**Current Version**: v0.8.5  
**Go Version**: 1.24.7+  
**Module**: github.com/cloudwego/eino

To check the version in your project:
```bash
grep github.com/cloudwego/eino ~/work/eino-examples/go.mod
```

---

## Common Import Aliases

```go
// Standard imports
import (
	"context"
	"io"
	"errors"
	
	// Eino core
	model "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/components"
	
	// Optional: specific implementations
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino-ext/components/model/ollama"
)

// Or with shorter names
import (
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Then use:
// model.BaseChatModel
// model.Option
// schema.Message
// schema.StreamReader
```

---

## Documentation Location

All source files with full documentation are at:
```
~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/
```

- `components/model/interface.go` - Chat model interfaces with examples
- `components/model/option.go` - Option types and constructors
- `schema/stream.go` - StreamReader with usage examples
- `components/types.go` - Component types and optional interfaces
