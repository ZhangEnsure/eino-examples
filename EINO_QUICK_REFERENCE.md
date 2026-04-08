# Eino Framework Quick Reference

## Module Cache Location
```
~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/
```

## Core Interfaces Quick Lookup

### 1. BaseChatModel (REQUIRED)
**File**: `components/model/interface.go:53-57`

```go
type BaseChatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.Message, error)
	Stream(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.StreamReader[*schema.Message], error)
}
```

### 2. ToolCallingChatModel (RECOMMENDED for tools)
**File**: `components/model/interface.go:85-91`

```go
type ToolCallingChatModel interface {
	BaseChatModel
	WithTools(tools []*schema.ToolInfo) (ToolCallingChatModel, error)
}
```

### 3. ChatModel (DEPRECATED - don't use!)
**File**: `components/model/interface.go:66-73`
- ⚠️ DEPRECATED: Use `ToolCallingChatModel` instead
- ⚠️ Not thread-safe (mutates receiver)

### 4. StreamReader[T]
**File**: `schema/stream.go:164-176`

```go
type StreamReader[T any] struct {
	typ readerType
	st *stream[T]
	ar *arrayReader[T]
	msr *multiStreamReader[T]
	srw *streamReaderWithConvert[T]
	csr *childStreamReader[T]
}

// Key methods:
// Recv() (T, error)
// Close() 
// Copy(n int) []*StreamReader[T]
```

### 5. Option
**File**: `components/model/option.go:42-50`

```go
type Option struct {
	apply func(opts *Options)
	implSpecificOptFn any
}

// Constructors:
WithTemperature(float32)
WithMaxTokens(int)
WithModel(string)
WithTopP(float32)
WithStop([]string)
WithTools([]*schema.ToolInfo)
WithToolChoice(schema.ToolChoice, ...string)
WrapImplSpecificOptFn[T any](func(*T))
```

### 6. Options
**File**: `components/model/option.go:22-40`

```go
type Options struct {
	Temperature *float32
	MaxTokens *int
	Model *string
	TopP *float32
	Stop []string
	Tools []*schema.ToolInfo
	ToolChoice *schema.ToolChoice
	AllowedToolNames []string
}
```

### 7. Component (string alias)
**File**: `components/types.go:64-83`

```go
type Component string

const (
	ComponentOfPrompt Component = "ChatTemplate"
	ComponentOfChatModel Component = "ChatModel"
	ComponentOfEmbedding Component = "Embedding"
	ComponentOfIndexer Component = "Indexer"
	ComponentOfRetriever Component = "Retriever"
	ComponentOfLoader Component = "Loader"
	ComponentOfTransformer Component = "DocumentTransformer"
	ComponentOfTool Component = "Tool"
)
```

### 8. Optional: Typer
**File**: `components/types.go:29-31`

```go
type Typer interface {
	GetType() string
}
```

### 9. Optional: Checker
**File**: `components/types.go:50-52`

```go
type Checker interface {
	IsCallbacksEnabled() bool
}
```

---

## Common Patterns

### Minimal ChatModel Implementation
```go
type MyModel struct {}

func (m *MyModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)
	// implementation
	return &schema.Message{}, nil
}

func (m *MyModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// implementation
	return schema.StreamReaderFromArray([]*schema.Message{}), nil
}
```

### With Tool Support
```go
func (m *MyModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &MyModel{tools: tools}, nil
}
```

### Using a Model
```go
resp, err := model.Generate(ctx, messages, 
	model.WithTemperature(0.7),
	model.WithMaxTokens(100),
)

reader, err := model.Stream(ctx, messages)
defer reader.Close()
for {
	chunk, err := reader.Recv()
	if errors.Is(err, io.EOF) { break }
	if err != nil { return err }
}
```

---

## Key Points

✅ **DO**:
- Implement `BaseChatModel` as the minimum
- Use `ToolCallingChatModel.WithTools()` for thread-safe tool binding
- Call `Close()` exactly once on `StreamReader`
- Use variadic `Option` parameters in implementations
- Compose options immutably

❌ **DON'T**:
- Use deprecated `ChatModel.BindTools()` 
- Mutate models in concurrent scenarios
- Forget to close `StreamReader`
- Use `StreamReader` from multiple goroutines

---

## File Structure in Module Cache
```
~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/
├── components/
│   ├── model/
│   │   ├── interface.go      ← BaseChatModel, ChatModel, ToolCallingChatModel
│   │   └── option.go         ← Option, Options
│   └── types.go              ← Component, Typer, Checker
├── schema/
│   └── stream.go             ← StreamReader[T]
└── ...
```
