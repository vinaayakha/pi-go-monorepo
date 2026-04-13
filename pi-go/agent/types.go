package agent

import (
	"context"

	"github.com/vinaayakha/pi-go/ai"
)

// ── Tool execution mode ─────────────────────────────────────────────

type ToolExecutionMode string

const (
	ToolExecSequential ToolExecutionMode = "sequential"
	ToolExecParallel   ToolExecutionMode = "parallel"
)

// ── Thinking level (agent-level, includes "off") ────────────────────

type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

// ── Agent tool result ───────────────────────────────────────────────

type AgentToolResult struct {
	Content []ai.ContentBlock `json:"content"`
	Details any               `json:"details,omitempty"`
}

// ── Agent tool ──────────────────────────────────────────────────────

// AgentTool defines a callable tool with typed parameters and execution logic.
type AgentTool struct {
	ai.Tool
	Label   string `json:"label"`
	Execute func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(AgentToolResult)) (AgentToolResult, error)
}

// ── Agent context ───────────────────────────────────────────────────

type AgentContext struct {
	SystemPrompt string       `json:"systemPrompt"`
	Messages     []ai.Message `json:"messages"`
	Tools        []AgentTool  `json:"tools,omitempty"`
}

// ── Before/after tool call hooks ────────────────────────────────────

type BeforeToolCallResult struct {
	Block  bool
	Reason string
}

type BeforeToolCallContext struct {
	AssistantMessage *ai.AssistantMessage
	ToolCall         *ai.ToolCall
	Args             map[string]any
	Context          *AgentContext
}

type AfterToolCallResult struct {
	Content []ai.ContentBlock
	Details any
	IsError *bool
}

type AfterToolCallContext struct {
	AssistantMessage *ai.AssistantMessage
	ToolCall         *ai.ToolCall
	Args             map[string]any
	Result           AgentToolResult
	IsError          bool
	Context          *AgentContext
}

// ── Agent loop config ───────────────────────────────────────────────

type AgentLoopConfig struct {
	Model ai.Model

	// ConvertToLLM transforms agent messages to LLM-compatible messages.
	ConvertToLLM func(messages []ai.Message) []ai.Message

	// TransformContext applies pre-processing to messages before ConvertToLLM.
	TransformContext func(ctx context.Context, messages []ai.Message) []ai.Message

	// GetAPIKey resolves an API key dynamically for each LLM call.
	GetAPIKey func(provider ai.Provider) string

	// GetSteeringMessages returns messages to inject mid-run.
	GetSteeringMessages func() []ai.Message

	// GetFollowUpMessages returns messages to process after the agent would stop.
	GetFollowUpMessages func() []ai.Message

	ToolExecution ToolExecutionMode
	BeforeToolCall func(ctx context.Context, btc BeforeToolCallContext) *BeforeToolCallResult
	AfterToolCall  func(ctx context.Context, atc AfterToolCallContext) *AfterToolCallResult

	// StreamOptions
	Reasoning       *ai.ThinkingLevel
	APIKey          string
	SessionID       string
	Transport       ai.Transport
	MaxRetryDelayMs int
}

// ── Agent events ────────────────────────────────────────────────────

type AgentEventType string

const (
	AgentEventStart          AgentEventType = "agent_start"
	AgentEventEnd            AgentEventType = "agent_end"
	TurnEventStart           AgentEventType = "turn_start"
	TurnEventEnd             AgentEventType = "turn_end"
	MessageEventStart        AgentEventType = "message_start"
	MessageEventUpdate       AgentEventType = "message_update"
	MessageEventEnd          AgentEventType = "message_end"
	ToolExecEventStart       AgentEventType = "tool_execution_start"
	ToolExecEventUpdate      AgentEventType = "tool_execution_update"
	ToolExecEventEnd         AgentEventType = "tool_execution_end"
)

type AgentEvent struct {
	Type AgentEventType `json:"type"`

	// For agent_end
	Messages []ai.Message `json:"messages,omitempty"`

	// For turn_end
	TurnMessage *ai.AssistantMessage   `json:"turnMessage,omitempty"`
	ToolResults []ai.ToolResultMessage `json:"toolResults,omitempty"`

	// For message_start, message_update, message_end
	Message *ai.Message `json:"message,omitempty"`

	// For message_update
	AssistantMessageEvent *ai.AssistantMessageEvent `json:"assistantMessageEvent,omitempty"`

	// For tool_execution_*
	ToolCallID string         `json:"toolCallId,omitempty"`
	ToolName   string         `json:"toolName,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Result     *AgentToolResult `json:"result,omitempty"`
	IsError    *bool          `json:"isError,omitempty"`
}
