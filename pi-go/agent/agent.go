package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vinaayakha/pi-go/ai"
	"github.com/vinaayakha/pi-go/threads"
)

// Agent is a stateful wrapper around the agent loop.
type Agent struct {
	mu sync.Mutex

	SystemPrompt  string
	Model         ai.Model
	ThinkingLevel ThinkingLevel
	ToolExecution ToolExecutionMode
	SessionID     string
	Transport     ai.Transport

	tools    []AgentTool
	messages []ai.Message

	isStreaming      bool
	streamingMessage *ai.Message
	pendingToolCalls map[string]struct{}
	errorMessage     string

	listeners      []func(AgentEvent)
	steeringQueue  []ai.Message
	followUpQueue  []ai.Message
	activeCancel   context.CancelFunc
	activeDone     chan struct{}

	ConvertToLLM     func([]ai.Message) []ai.Message
	TransformContext func(context.Context, []ai.Message) []ai.Message
	GetAPIKey        func(ai.Provider) string
	BeforeToolCall   func(context.Context, BeforeToolCallContext) *BeforeToolCallResult
	AfterToolCall    func(context.Context, AfterToolCallContext) *AfterToolCallResult

	// Thread persistence
	ThreadStore threads.Store
	threadID    string
}

// NewAgent creates a new Agent with default settings.
func NewAgent(model ai.Model) *Agent {
	return &Agent{
		Model:            model,
		ThinkingLevel:    ThinkingOff,
		ToolExecution:    ToolExecParallel,
		Transport:        ai.TransportSSE,
		pendingToolCalls: map[string]struct{}{},
	}
}

// SetTools replaces the tool set.
func (a *Agent) SetTools(tools []AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = append([]AgentTool{}, tools...)
}

// Tools returns a copy of the current tools.
func (a *Agent) Tools() []AgentTool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AgentTool{}, a.tools...)
}

// SetMessages replaces the message history.
func (a *Agent) SetMessages(msgs []ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append([]ai.Message{}, msgs...)
}

// Messages returns a copy of the message history.
func (a *Agent) Messages() []ai.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ai.Message{}, a.messages...)
}

// IsStreaming returns true while the agent is processing.
func (a *Agent) IsStreaming() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isStreaming
}

// ErrorMessage returns the error from the most recent failed turn.
func (a *Agent) ErrorMessage() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.errorMessage
}

// Subscribe adds an event listener. Returns an unsubscribe function.
func (a *Agent) Subscribe(listener func(AgentEvent)) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listeners = append(a.listeners, listener)
	idx := len(a.listeners) - 1
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.listeners = append(a.listeners[:idx], a.listeners[idx+1:]...)
	}
}

// Steer queues a message to inject after the current turn.
func (a *Agent) Steer(msg ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = append(a.steeringQueue, msg)
}

// FollowUp queues a message to run after the agent would stop.
func (a *Agent) FollowUp(msg ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue = append(a.followUpQueue, msg)
}

// Abort cancels the active run.
func (a *Agent) Abort() {
	a.mu.Lock()
	cancel := a.activeCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// WaitForIdle blocks until the current run finishes.
func (a *Agent) WaitForIdle() {
	a.mu.Lock()
	done := a.activeDone
	a.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Reset clears all state.
func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = nil
	a.isStreaming = false
	a.streamingMessage = nil
	a.pendingToolCalls = map[string]struct{}{}
	a.errorMessage = ""
	a.steeringQueue = nil
	a.followUpQueue = nil
}

// ── Thread persistence ──────────────────────────────────────────────

// ThreadID returns the current thread ID, if any.
func (a *Agent) ThreadID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.threadID
}

// NewThread creates a new thread and associates it with this agent.
func (a *Agent) NewThread(metadata map[string]string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ThreadStore == nil {
		return "", fmt.Errorf("no thread store configured")
	}
	t, err := a.ThreadStore.Create(metadata)
	if err != nil {
		return "", err
	}
	a.threadID = t.ID
	a.messages = nil
	return t.ID, nil
}

// LoadThread loads a thread from the store and sets it as the current conversation.
func (a *Agent) LoadThread(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ThreadStore == nil {
		return fmt.Errorf("no thread store configured")
	}
	t, err := a.ThreadStore.Get(id)
	if err != nil {
		return err
	}
	a.threadID = t.ID
	a.messages = append([]ai.Message{}, t.Messages...)
	return nil
}

// SaveThread persists the current conversation to the thread store.
func (a *Agent) SaveThread() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ThreadStore == nil || a.threadID == "" {
		return nil // no-op if no store or no thread
	}
	return a.ThreadStore.SetMessages(a.threadID, a.messages)
}

// Prompt starts a new run with the given text.
func (a *Agent) Prompt(ctx context.Context, text string) error {
	msg := ai.Message{
		User: &ai.UserMessage{
			Role: "user",
			Content: []ai.ContentBlock{
				{Text: &ai.TextContent{Type: "text", Text: text}},
			},
			Timestamp: time.Now().UnixMilli(),
		},
	}
	return a.PromptMessages(ctx, []ai.Message{msg})
}

// PromptMessages starts a new run with the given messages.
func (a *Agent) PromptMessages(ctx context.Context, messages []ai.Message) error {
	a.mu.Lock()
	if a.isStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.activeCancel = cancel
	a.activeDone = done
	a.isStreaming = true
	a.errorMessage = ""

	agentCtx := &AgentContext{
		SystemPrompt: a.SystemPrompt,
		Messages:     append([]ai.Message{}, a.messages...),
		Tools:        append([]AgentTool{}, a.tools...),
	}
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.isStreaming = false
			a.streamingMessage = nil
			a.pendingToolCalls = map[string]struct{}{}
			a.activeCancel = nil
			a.activeDone = nil
			a.mu.Unlock()
			cancel()
			close(done)
		}()

		config := a.buildConfig()
		newMessages := RunAgentLoop(runCtx, messages, agentCtx, config, func(event AgentEvent) {
			a.processEvent(event)
		})
		_ = newMessages
	}()

	return nil
}

func (a *Agent) buildConfig() *AgentLoopConfig {
	a.mu.Lock()
	defer a.mu.Unlock()

	var reasoning *ai.ThinkingLevel
	if a.ThinkingLevel != ThinkingOff {
		r := ai.ThinkingLevel(a.ThinkingLevel)
		reasoning = &r
	}

	return &AgentLoopConfig{
		Model:         a.Model,
		ConvertToLLM:  a.ConvertToLLM,
		TransformContext: a.TransformContext,
		GetAPIKey:     a.GetAPIKey,
		BeforeToolCall: a.BeforeToolCall,
		AfterToolCall: a.AfterToolCall,
		ToolExecution: a.ToolExecution,
		Reasoning:     reasoning,
		SessionID:     a.SessionID,
		Transport:     a.Transport,
		GetSteeringMessages: func() []ai.Message {
			a.mu.Lock()
			defer a.mu.Unlock()
			msgs := a.steeringQueue
			a.steeringQueue = nil
			return msgs
		},
		GetFollowUpMessages: func() []ai.Message {
			a.mu.Lock()
			defer a.mu.Unlock()
			msgs := a.followUpQueue
			a.followUpQueue = nil
			return msgs
		},
	}
}

func (a *Agent) processEvent(event AgentEvent) {
	a.mu.Lock()

	switch event.Type {
	case MessageEventStart:
		a.streamingMessage = event.Message
	case MessageEventUpdate:
		a.streamingMessage = event.Message
	case MessageEventEnd:
		a.streamingMessage = nil
		if event.Message != nil {
			a.messages = append(a.messages, *event.Message)
		}
	case ToolExecEventStart:
		a.pendingToolCalls[event.ToolCallID] = struct{}{}
	case ToolExecEventEnd:
		delete(a.pendingToolCalls, event.ToolCallID)
	case TurnEventEnd:
		if event.TurnMessage != nil && event.TurnMessage.ErrorMessage != "" {
			a.errorMessage = event.TurnMessage.ErrorMessage
		}
	case AgentEventEnd:
		a.streamingMessage = nil
		// Auto-save thread if configured
		if a.ThreadStore != nil && a.threadID != "" {
			_ = a.ThreadStore.SetMessages(a.threadID, a.messages)
		}
	}

	listeners := append([]func(AgentEvent){}, a.listeners...)
	a.mu.Unlock()

	for _, l := range listeners {
		l(event)
	}
}
