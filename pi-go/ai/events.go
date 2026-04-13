package ai

// AssistantMessageEventType enumerates the streaming event types.
type AssistantMessageEventType string

const (
	EventStart         AssistantMessageEventType = "start"
	EventTextStart     AssistantMessageEventType = "text_start"
	EventTextDelta     AssistantMessageEventType = "text_delta"
	EventTextEnd       AssistantMessageEventType = "text_end"
	EventThinkingStart AssistantMessageEventType = "thinking_start"
	EventThinkingDelta AssistantMessageEventType = "thinking_delta"
	EventThinkingEnd   AssistantMessageEventType = "thinking_end"
	EventToolCallStart AssistantMessageEventType = "toolcall_start"
	EventToolCallDelta AssistantMessageEventType = "toolcall_delta"
	EventToolCallEnd   AssistantMessageEventType = "toolcall_end"
	EventDone          AssistantMessageEventType = "done"
	EventError         AssistantMessageEventType = "error"
)

// AssistantMessageEvent is emitted during streaming.
type AssistantMessageEvent struct {
	Type         AssistantMessageEventType `json:"type"`
	ContentIndex int                       `json:"contentIndex,omitempty"`
	Delta        string                    `json:"delta,omitempty"`
	Content      string                    `json:"content,omitempty"`
	ToolCall     *ToolCall                 `json:"toolCall,omitempty"`
	Partial      *AssistantMessage         `json:"partial,omitempty"`
	Message      *AssistantMessage         `json:"message,omitempty"`
	Reason       StopReason                `json:"reason,omitempty"`
	Error        *AssistantMessage         `json:"error,omitempty"`
}
