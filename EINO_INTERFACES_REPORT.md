# Eino Framework Interface Definitions Report
**Project**: eino-examples  
**Framework Version**: v0.8.5  
**Date**: 2026-04-07

---

## Summary

This report documents the core ChatModel interfaces, Component types, StreamReader types, and Option types from the Eino framework (v0.8.5). All definitions are found in the Go module cache at `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/`.

---

## 1. BaseChatModel Interface

**File Path**: `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/components/model/interface.go` (lines 53-57)

**Definition**:
```go
// BaseChatModel defines the core interface for all chat model implementations.
//
// It exposes two modes of interaction:
//   - [BaseChatModel.Generate]: blocks until the model returns a complete response.
//   - [BaseChatModel.Stream]: returns a [schema.StreamReader] that yields message
//     chunks incrementally as the model generates them.
//
// The input is a slice of [schema.Message] representing the conversation history.
// Messages carry a role (system, user, assistant, tool) and support multimodal
// content (text, images, audio, video). Tool messages must include a ToolCallID
// that correlates them with a prior assistant tool-call message.
//
// Stream usage — the caller is responsible for closing the reader:
//
//	reader, err := m.Stream(ctx, messages)
//	if err != nil { ... }
//	defer reader.Close()
//	for {
//	    chunk, err := reader.Recv()
//	    if errors.Is(err, io.EOF) { break }
//	    if err != nil { ... }
//	    // handle chunk
//	}
//
// Note: a [schema.StreamReader] can only be read once. If multiple consumers
// need the stream, it must be copied before reading.
//
//go:generate  mockgen -destination ../../internal/mock/components/model/ChatModel_mock.go --package model -source interface.go
type BaseChatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...Option) (*schema.Message, error)
	Stream(ctx context.Context, input []*schema.Message, opts ...Option) (
		*schema.StreamReader[*schema.Message], error)
}
```

**Key Points**:
- Two primary methods: `Generate()` (blocking) and `Stream()` (non-blocking with streaming)
- Both methods accept context, message slice, and variadic Option parameters
- Generate returns a single `*schema.Message`
- Stream returns a `*schema.StreamReader[*schema.Message]` for streaming chunks
- Messages support multimodal content (text, images, audio, video)

---

## 2. ChatModel Interface (Deprecated)

**File Path**: `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/components/model/interface.go` (lines 59-73)

**Definition**:
```go
// Deprecated: Use [ToolCallingChatModel] instead.
//
// ChatModel extends [BaseChatModel] with tool binding via [ChatModel.BindTools].
// BindTools mutates the instance in place, which causes a race condition when
// the same instance is used concurrently: one goroutine's tool list can
// overwrite another's. Prefer [ToolCallingChatModel.WithTools], which returns
// a new immutable instance and is safe for concurrent use.
type ChatModel interface {
	BaseChatModel

	// BindTools bind tools to the model.
	// BindTools before requesting ChatModel generally.
	// notice the non-atomic problem of BindTools and Generate.
	BindTools(tools []*schema.ToolInfo) error
}
```

**Key Points**:
- **DEPRECATED** - Use `ToolCallingChatModel` instead
- Extends `BaseChatModel`
- Adds `BindTools()` method for tool binding
- **Warning**: BindTools mutates the receiver, causing race conditions in concurrent use
- Use `ToolCallingChatModel.WithTools()` for thread-safe alternatives

---

## 3. ToolCallingChatModel Interface (Recommended)

**File Path**: `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/components/model/interface.go` (lines 75-91)

**Definition**:
```go
// ToolCallingChatModel extends [BaseChatModel] with safe tool binding.
//
// Unlike the deprecated [ChatModel.BindTools], [ToolCallingChatModel.WithTools]
// does not mutate the receiver — it returns a new instance with the given tools
// attached. This makes it safe to share a base model instance across goroutines
// and derive per-request variants with different tool sets:
//
//	base, _ := openai.NewChatModel(ctx, cfg)           // shared, no tools
//	withSearch, _ := base.WithTools([]*schema.ToolInfo{searchTool})
//	withCalc, _  := base.WithTools([]*schema.ToolInfo{calcTool})
type ToolCallingChatModel interface {
	BaseChatModel

	// WithTools returns a new ToolCallingChatModel instance with the specified tools bound.
	// This method does not modify the current instance, making it safer for concurrent use.
	WithTools(tools []*schema.ToolInfo) (ToolCallingChatModel, error)
}
```

**Key Points**:
- **Recommended** replacement for deprecated `ChatModel`
- Extends `BaseChatModel`
- Adds `WithTools()` method for immutable tool binding
- **Thread-safe**: Returns new instance, doesn't mutate receiver
- Enables safe sharing of base model across goroutines
- Can derive per-request variants with different tool sets

---

## 4. StreamReader Type

**File Path**: `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/schema/stream.go` (lines 164-176)

**Definition**:
```go
// StreamReader is the consumer side of an Eino stream.
//
// A StreamReader is read-once: only one goroutine should call Recv, and the
// reader must be closed exactly once (whether the loop finishes normally or
// exits early via break or return).
//
// Typical usage:
//
//	defer sr.Close() // always close, even after io.EOF
//	for {
//	    chunk, err := sr.Recv()
//	    if errors.Is(err, io.EOF) {
//	        break
//	    }
//	    if err != nil {
//	        return err
//	    }
//	    process(chunk)
//	}
//
// To fan-out a single stream to N independent consumers, call [StreamReader.Copy]
// before any Recv; the original reader becomes unusable after the call.
//
// StreamReaders are created by [Pipe], [StreamReaderFromArray],
// [MergeStreamReaders], [MergeNamedStreamReaders], and [StreamReaderWithConvert].
type StreamReader[T any] struct {
	typ readerType

	st *stream[T]

	ar *arrayReader[T]

	msr *multiStreamReader[T]

	srw *streamReaderWithConvert[T]

	csr *childStreamReader[T]
}
```

**Key Methods**:
- `Recv() (T, error)` - Receives next value from stream
- `Close()` - Closes the reader (must be called exactly once)
- `Copy(n int) []*StreamReader[T]` - Creates n independent copies for fan-out

**Key Points**:
- Generic type `StreamReader[T any]`
- **Read-once**: Only one goroutine should call Recv
- **Must close**: Call Close exactly once (even after io.EOF)
- Can be copied for multiple independent consumers
- Multiple internal implementations (stream, array, multiStream, convert, child)
- Returns `io.EOF` when stream is exhausted

---

## 5. Option Type

**File Path**: `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/components/model/option.go` (lines 42-50)

**Definition**:
```go
// Options is the common options for the model.
type Options struct {
	// Temperature is the temperature for the model, which controls the randomness of the model.
	Temperature *float32
	// MaxTokens is the max number of tokens, if reached the max tokens, the model will stop generating, and mostly return an finish reason of "length".
	MaxTokens *int
	// Model is the model name.
	Model *string
	// TopP is the top p for the model, which controls the diversity of the model.
	TopP *float32
	// Stop is the stop words for the model, which controls the stopping condition of the model.
	Stop []string
	// Tools is a list of tools the model may call.
	Tools []*schema.ToolInfo
	// ToolChoice controls which tool is called by the model.
	ToolChoice *schema.ToolChoice
	// AllowedToolNames specifies a list of tool names that the model is allowed to call.
	// This allows for constraining the model to a specific subset of the available tools.
	AllowedToolNames []string
}

// Option is a call-time option for a ChatModel. Options are immutable and
// composable: each Option carries either a common-option setter (applied via
// [GetCommonOptions]) or an implementation-specific setter (applied via
// [GetImplSpecificOptions]), never both.
type Option struct {
	apply func(opts *Options)

	implSpecificOptFn any
}
```

**Available Option Constructors**:
- `WithTemperature(float32)` - Set temperature for randomness control
- `WithMaxTokens(int)` - Set maximum token limit
- `WithModel(string)` - Set model name
- `WithTopP(float32)` - Set top-p for diversity control
- `WithStop([]string)` - Set stop words
- `WithTools([]*schema.ToolInfo)` - Set tools
- `WithToolChoice(schema.ToolChoice, ...string)` - Set tool choice and allowed tool names
- `WrapImplSpecificOptFn[T any](func(*T))` - Wrap implementation-specific options

**Key Functions**:
- `GetCommonOptions(base *Options, opts ...Option) *Options` - Extract and merge common options
- `GetImplSpecificOptions[T any](base *T, opts ...Option) *T` - Extract and merge implementation-specific options

**Key Points**:
- **Immutable and composable**: Each Option can be mixed with others
- Supports both standard and implementation-specific options
- Options are variadic parameters in Generate/Stream methods
- Used by model implementations to customize behavior at call time

---

## 6. Component Type

**File Path**: `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/components/types.go` (lines 64-83)

**Definition**:
```go
// Component names representing the different categories of components.
type Component string

const (
	// ComponentOfPrompt identifies chat template components.
	ComponentOfPrompt Component = "ChatTemplate"
	// ComponentOfChatModel identifies chat model components.
	ComponentOfChatModel Component = "ChatModel"
	// ComponentOfEmbedding identifies embedding components.
	ComponentOfEmbedding Component = "Embedding"
	// ComponentOfIndexer identifies indexer components.
	ComponentOfIndexer Component = "Indexer"
	// ComponentOfRetriever identifies retriever components.
	ComponentOfRetriever Component = "Retriever"
	// ComponentOfLoader identifies loader components.
	ComponentOfLoader Component = "Loader"
	// ComponentOfTransformer identifies document transformer components.
	ComponentOfTransformer Component = "DocumentTransformer"
	// ComponentOfTool identifies tool components.
	ComponentOfTool Component = "Tool"
)
```

**Key Points**:
- `Component` is a **string alias type**, not an interface
- Represents different categories/types of components
- Used for component identification and classification
- Related interfaces: `Typer` and `Checker` (see below)

---

## 7. Supporting Interfaces in components/types.go

**File Path**: `~/go/pkg/mod/github.com/cloudwego/eino@v0.8.5/components/types.go`

### Typer Interface (lines 21-40)
```go
// Typer provides a human-readable type name for a component implementation.
//
// When implemented, the component's full display name in DevOps tooling
// (visual debugger, IDE plugin, dashboards) becomes "{GetType()}{ComponentKind}"
// — e.g. "OpenAIChatModel". Use CamelCase naming.
//
// Also used by [utils.InferTool] and similar constructors to set the display
// name of tool instances.
type Typer interface {
	GetType() string
}
```

### Checker Interface (lines 50-61)
```go
// Checker controls whether the framework's automatic callback instrumentation
// is active for a component.
//
// When IsCallbacksEnabled returns true, the framework skips its default
// OnStart/OnEnd wrapping and trusts the component to invoke callbacks itself
// at the correct points. Implement this when your component needs precise
// control over callback timing or content — for example, when streaming
// requires callbacks to fire mid-stream rather than only at completion.
type Checker interface {
	IsCallbacksEnabled() bool
}
```

**Key Points**:
- **Typer**: Optional interface for human-readable component names
- **Checker**: Optional interface to control callback instrumentation
- Components don't inherit from these but can opt-in by implementing them

---

## Usage Patterns

### Creating a Custom ChatModel Implementation

Your implementation should:

1. **Implement BaseChatModel** (minimum):
```go
type MyModel struct {}

func (m *MyModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
    options := model.GetCommonOptions(nil, opts...)
    // Use options.Temperature, options.Tools, etc.
    // Return response message
}

func (m *MyModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
    // Return streaming reader
}
```

2. **Optionally implement ToolCallingChatModel** (if supporting tools):
```go
func (m *MyModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
    // Return new instance with tools (don't mutate receiver)
    return &MyModel{tools: tools}, nil
}
```

3. **Optionally implement optional interfaces**:
```go
func (m *MyModel) GetType() string { return "MyModel" }  // Typer
func (m *MyModel) IsCallbacksEnabled() bool { return true }  // Checker
```

### Using a ChatModel

```go
ctx := context.Background()
messages := []*schema.Message{...}

// Generate a response
response, err := model.Generate(ctx, messages, 
    model.WithTemperature(0.7),
    model.WithMaxTokens(100),
    model.WithTools(tools),
)

// Stream a response
reader, err := model.Stream(ctx, messages,
    model.WithTemperature(0.7),
)
defer reader.Close()
for {
    chunk, err := reader.Recv()
    if errors.Is(err, io.EOF) { break }
    if err != nil { return err }
    process(chunk)
}
```

---

## File Location Summary

| Item | File Path | Lines |
|------|-----------|-------|
| BaseChatModel | `components/model/interface.go` | 53-57 |
| ChatModel (deprecated) | `components/model/interface.go` | 66-73 |
| ToolCallingChatModel | `components/model/interface.go` | 85-91 |
| Option | `components/model/option.go` | 42-50 |
| Options | `components/model/option.go` | 22-40 |
| StreamReader | `schema/stream.go` | 164-176 |
| Component | `components/types.go` | 64-83 |
| Typer | `components/types.go` | 29-31 |
| Checker | `components/types.go` | 50-52 |

---

## Key Dependencies

```
github.com/cloudwego/eino/schema
  ├── Message
  ├── StreamReader[T]
  ├── ToolInfo
  └── ToolChoice

github.com/cloudwego/eino/components/model
  ├── BaseChatModel
  ├── ChatModel (deprecated)
  ├── ToolCallingChatModel
  ├── Option
  └── Options

github.com/cloudwego/eino/components
  ├── Component (string type)
  ├── Typer
  └── Checker
```

---

## Important Notes

1. **Thread Safety**: Use `ToolCallingChatModel.WithTools()` instead of deprecated `ChatModel.BindTools()` for concurrent use
2. **StreamReader**: Must be closed exactly once, is read-once (single goroutine)
3. **Component Interface**: `Component` is a **string alias type**, not a Go interface
4. **Options**: Use variadic Option parameters, which are composable and immutable
5. **Callbacks**: Implement `Checker.IsCallbacksEnabled()` if you need custom callback control

