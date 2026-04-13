package ai

import (
	"context"
	"encoding/json"
	"time"
)

// ── API / Provider identifiers ──────────────────────────────────────

type API string

const (
	APIOpenAICompletions     API = "openai-completions"
	APIOpenAIResponses       API = "openai-responses"
	APIMistralConversations  API = "mistral-conversations"
	APIAnthropicMessages     API = "anthropic-messages"
	APIBedrockConverseStream API = "bedrock-converse-stream"
	APIGoogleGenerativeAI    API = "google-generative-ai"
	APIGoogleGeminiCLI       API = "google-gemini-cli"
	APIGoogleVertex          API = "google-vertex"
	APIAzureOpenAIResponses  API = "azure-openai-responses"
)

type Provider string

const (
	ProviderAmazonBedrock Provider = "amazon-bedrock"
	ProviderAnthropic     Provider = "anthropic"
	ProviderGoogle        Provider = "google"
	ProviderOpenAI        Provider = "openai"
	ProviderXAI           Provider = "xai"
	ProviderGroq          Provider = "groq"
	ProviderMistral       Provider = "mistral"
	ProviderOpenRouter    Provider = "openrouter"
)

// ── Thinking / Reasoning ────────────────────────────────────────────

type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

type ThinkingBudgets struct {
	Minimal *int `json:"minimal,omitempty"`
	Low     *int `json:"low,omitempty"`
	Medium  *int `json:"medium,omitempty"`
	High    *int `json:"high,omitempty"`
}

// ── Stream options ──────────────────────────────────────────────────

type CacheRetention string

const (
	CacheNone  CacheRetention = "none"
	CacheShort CacheRetention = "short"
	CacheLong  CacheRetention = "long"
)

type Transport string

const (
	TransportSSE       Transport = "sse"
	TransportWebSocket Transport = "websocket"
	TransportAuto      Transport = "auto"
)

// StreamOptions are the base options shared by all providers.
type StreamOptions struct {
	Temperature      *float64          `json:"temperature,omitempty"`
	MaxTokens        *int              `json:"maxTokens,omitempty"`
	Ctx              context.Context   `json:"-"`
	APIKey           string            `json:"apiKey,omitempty"`
	Transport        Transport         `json:"transport,omitempty"`
	CacheRetention   CacheRetention    `json:"cacheRetention,omitempty"`
	SessionID        string            `json:"sessionId,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	MaxRetryDelayMs  int               `json:"maxRetryDelayMs,omitempty"`
	Metadata         map[string]any    `json:"metadata,omitempty"`
}

// SimpleStreamOptions extends StreamOptions with reasoning support.
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       *ThinkingLevel  `json:"reasoning,omitempty"`
	ThinkingBudgets *ThinkingBudgets `json:"thinkingBudgets,omitempty"`
}

// ── Content types ───────────────────────────────────────────────────

type TextContent struct {
	Type          string `json:"type"` // always "text"
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

type ThinkingContent struct {
	Type              string `json:"type"` // always "thinking"
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`
}

type ImageContent struct {
	Type     string `json:"type"` // always "image"
	Data     string `json:"data"` // base64
	MimeType string `json:"mimeType"`
}

type ToolCall struct {
	Type             string         `json:"type"` // always "toolCall"
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Arguments        map[string]any `json:"arguments"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

// ContentBlock is a union of text, thinking, image, and tool-call blocks.
type ContentBlock struct {
	// Exactly one of these will be non-nil.
	Text     *TextContent     `json:"text,omitempty"`
	Thinking *ThinkingContent `json:"thinking,omitempty"`
	Image    *ImageContent    `json:"image,omitempty"`
	ToolCall *ToolCall        `json:"toolCall,omitempty"`
}

// ── Usage / Cost ────────────────────────────────────────────────────

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type Usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	TotalTokens int  `json:"totalTokens"`
	Cost        Cost `json:"cost"`
}

func EmptyUsage() Usage {
	return Usage{}
}

// ── Stop reason ─────────────────────────────────────────────────────

type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// ── Messages ────────────────────────────────────────────────────────

type UserMessage struct {
	Role      string         `json:"role"` // "user"
	Content   []ContentBlock `json:"content"`
	Timestamp int64          `json:"timestamp"` // Unix ms
}

func NewUserMessage(text string) UserMessage {
	return UserMessage{
		Role: "user",
		Content: []ContentBlock{
			{Text: &TextContent{Type: "text", Text: text}},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}

type AssistantMessage struct {
	Role         string         `json:"role"` // "assistant"
	Content      []ContentBlock `json:"content"`
	API          API            `json:"api"`
	Provider     Provider       `json:"provider"`
	Model        string         `json:"model"`
	ResponseID   string         `json:"responseId,omitempty"`
	Usage        Usage          `json:"usage"`
	StopReason   StopReason     `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Timestamp    int64          `json:"timestamp"`
}

type ToolResultMessage struct {
	Role       string         `json:"role"` // "toolResult"
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Content    []ContentBlock `json:"content"`
	Details    any            `json:"details,omitempty"`
	IsError    bool           `json:"isError"`
	Timestamp  int64          `json:"timestamp"`
}

// Message is a union of user, assistant, and tool-result messages.
type Message struct {
	User      *UserMessage       `json:",omitempty"`
	Assistant *AssistantMessage   `json:",omitempty"`
	ToolResult *ToolResultMessage `json:",omitempty"`
}

func (m Message) Role() string {
	switch {
	case m.User != nil:
		return "user"
	case m.Assistant != nil:
		return "assistant"
	case m.ToolResult != nil:
		return "toolResult"
	default:
		return ""
	}
}

// MarshalJSON implements custom JSON encoding for the Message union.
func (m Message) MarshalJSON() ([]byte, error) {
	switch {
	case m.User != nil:
		return json.Marshal(m.User)
	case m.Assistant != nil:
		return json.Marshal(m.Assistant)
	case m.ToolResult != nil:
		return json.Marshal(m.ToolResult)
	default:
		return []byte("null"), nil
	}
}

// ── Tool definition ─────────────────────────────────────────────────

// Tool describes a tool the LLM can call.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ── Context ─────────────────────────────────────────────────────────

// Context is the payload sent to the LLM.
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

// ── Model ───────────────────────────────────────────────────────────

type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type Model struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	API           API               `json:"api"`
	Provider      Provider          `json:"provider"`
	BaseURL       string            `json:"baseUrl"`
	Reasoning     bool              `json:"reasoning"`
	Input         []string          `json:"input"` // "text", "image"
	Cost          ModelCost         `json:"cost"`
	ContextWindow int               `json:"contextWindow"`
	MaxTokens     int               `json:"maxTokens"`
	Headers       map[string]string `json:"headers,omitempty"`
}
