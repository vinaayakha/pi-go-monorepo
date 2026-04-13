package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vinaayakha/pi-go/ai"
)

// AgentEventSink receives events from the agent loop.
type AgentEventSink func(event AgentEvent)

// RunAgentLoop starts a new loop with prompt messages.
func RunAgentLoop(
	ctx context.Context,
	prompts []ai.Message,
	agentCtx *AgentContext,
	config *AgentLoopConfig,
	emit AgentEventSink,
) []ai.Message {
	newMessages := append([]ai.Message{}, prompts...)
	currentCtx := &AgentContext{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     append(append([]ai.Message{}, agentCtx.Messages...), prompts...),
		Tools:        agentCtx.Tools,
	}

	emit(AgentEvent{Type: AgentEventStart})
	emit(AgentEvent{Type: TurnEventStart})
	for _, p := range prompts {
		emit(AgentEvent{Type: MessageEventStart, Message: &p})
		emit(AgentEvent{Type: MessageEventEnd, Message: &p})
	}

	runLoop(ctx, currentCtx, &newMessages, config, emit)
	return newMessages
}

func runLoop(
	ctx context.Context,
	currentCtx *AgentContext,
	newMessages *[]ai.Message,
	config *AgentLoopConfig,
	emit AgentEventSink,
) {
	firstTurn := true
	var pendingMessages []ai.Message
	if config.GetSteeringMessages != nil {
		pendingMessages = config.GetSteeringMessages()
	}

	for {
		hasMoreToolCalls := true
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if !firstTurn {
				emit(AgentEvent{Type: TurnEventStart})
			} else {
				firstTurn = false
			}

			// Inject pending messages
			if len(pendingMessages) > 0 {
				for _, m := range pendingMessages {
					emit(AgentEvent{Type: MessageEventStart, Message: &m})
					emit(AgentEvent{Type: MessageEventEnd, Message: &m})
					currentCtx.Messages = append(currentCtx.Messages, m)
					*newMessages = append(*newMessages, m)
				}
				pendingMessages = nil
			}

			// Stream assistant response
			msg := streamAssistantResponse(ctx, currentCtx, config, emit)
			*newMessages = append(*newMessages, ai.Message{Assistant: msg})

			if msg.StopReason == ai.StopReasonError || msg.StopReason == ai.StopReasonAborted {
				emit(AgentEvent{Type: TurnEventEnd, TurnMessage: msg})
				emit(AgentEvent{Type: AgentEventEnd, Messages: *newMessages})
				return
			}

			// Check for tool calls
			var toolCalls []*ai.ToolCall
			for i := range msg.Content {
				if msg.Content[i].ToolCall != nil {
					toolCalls = append(toolCalls, msg.Content[i].ToolCall)
				}
			}
			hasMoreToolCalls = len(toolCalls) > 0

			var toolResults []ai.ToolResultMessage
			if hasMoreToolCalls {
				toolResults = executeToolCalls(ctx, currentCtx, msg, toolCalls, config, emit)
				for _, tr := range toolResults {
					m := ai.Message{ToolResult: &tr}
					currentCtx.Messages = append(currentCtx.Messages, m)
					*newMessages = append(*newMessages, m)
				}
			}

			emit(AgentEvent{Type: TurnEventEnd, TurnMessage: msg, ToolResults: toolResults})

			if config.GetSteeringMessages != nil {
				pendingMessages = config.GetSteeringMessages()
			}
		}

		// Check for follow-up messages
		if config.GetFollowUpMessages != nil {
			followUp := config.GetFollowUpMessages()
			if len(followUp) > 0 {
				pendingMessages = followUp
				continue
			}
		}
		break
	}

	emit(AgentEvent{Type: AgentEventEnd, Messages: *newMessages})
}

func streamAssistantResponse(
	ctx context.Context,
	agentCtx *AgentContext,
	config *AgentLoopConfig,
	emit AgentEventSink,
) *ai.AssistantMessage {
	messages := agentCtx.Messages
	if config.TransformContext != nil {
		messages = config.TransformContext(ctx, messages)
	}

	llmMessages := messages
	if config.ConvertToLLM != nil {
		llmMessages = config.ConvertToLLM(messages)
	}

	// Build tools list
	var tools []ai.Tool
	for _, t := range agentCtx.Tools {
		tools = append(tools, t.Tool)
	}

	llmCtx := ai.Context{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     llmMessages,
		Tools:        tools,
	}

	apiKey := config.APIKey
	if config.GetAPIKey != nil {
		if k := config.GetAPIKey(config.Model.Provider); k != "" {
			apiKey = k
		}
	}

	opts := &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			APIKey:    apiKey,
			Ctx:       ctx,
			SessionID: config.SessionID,
			Transport: config.Transport,
		},
		Reasoning: config.Reasoning,
	}

	es, err := ai.StreamSimple(config.Model, llmCtx, opts)
	if err != nil {
		return &ai.AssistantMessage{
			Role:         "assistant",
			API:          config.Model.API,
			Provider:     config.Model.Provider,
			Model:        config.Model.ID,
			StopReason:   ai.StopReasonError,
			ErrorMessage: err.Error(),
			Timestamp:    time.Now().UnixMilli(),
		}
	}

	var final *ai.AssistantMessage
	started := false

	for event := range es.Events() {
		switch event.Type {
		case ai.EventStart:
			started = true
			m := ai.Message{Assistant: event.Partial}
			agentCtx.Messages = append(agentCtx.Messages, m)
			emit(AgentEvent{Type: MessageEventStart, Message: &m})

		case ai.EventTextStart, ai.EventTextDelta, ai.EventTextEnd,
			ai.EventThinkingStart, ai.EventThinkingDelta, ai.EventThinkingEnd,
			ai.EventToolCallStart, ai.EventToolCallDelta, ai.EventToolCallEnd:
			if event.Partial != nil {
				m := ai.Message{Assistant: event.Partial}
				if len(agentCtx.Messages) > 0 {
					agentCtx.Messages[len(agentCtx.Messages)-1] = m
				}
				emit(AgentEvent{Type: MessageEventUpdate, Message: &m, AssistantMessageEvent: &event})
			}

		case ai.EventDone:
			final = event.Message
		case ai.EventError:
			final = event.Error
		}
	}

	if final == nil {
		final = es.Result()
	}

	if final != nil {
		m := ai.Message{Assistant: final}
		if started && len(agentCtx.Messages) > 0 {
			agentCtx.Messages[len(agentCtx.Messages)-1] = m
		} else {
			agentCtx.Messages = append(agentCtx.Messages, m)
		}
		if !started {
			emit(AgentEvent{Type: MessageEventStart, Message: &m})
		}
		emit(AgentEvent{Type: MessageEventEnd, Message: &m})
	}

	return final
}

func executeToolCalls(
	ctx context.Context,
	agentCtx *AgentContext,
	assistantMsg *ai.AssistantMessage,
	toolCalls []*ai.ToolCall,
	config *AgentLoopConfig,
	emit AgentEventSink,
) []ai.ToolResultMessage {
	if config.ToolExecution == ToolExecSequential {
		return executeSequential(ctx, agentCtx, assistantMsg, toolCalls, config, emit)
	}
	return executeParallel(ctx, agentCtx, assistantMsg, toolCalls, config, emit)
}

func executeSequential(
	ctx context.Context,
	agentCtx *AgentContext,
	assistantMsg *ai.AssistantMessage,
	toolCalls []*ai.ToolCall,
	config *AgentLoopConfig,
	emit AgentEventSink,
) []ai.ToolResultMessage {
	var results []ai.ToolResultMessage
	for _, tc := range toolCalls {
		result := executeSingleToolCall(ctx, agentCtx, assistantMsg, tc, config, emit)
		results = append(results, result)
	}
	return results
}

func executeParallel(
	ctx context.Context,
	agentCtx *AgentContext,
	assistantMsg *ai.AssistantMessage,
	toolCalls []*ai.ToolCall,
	config *AgentLoopConfig,
	emit AgentEventSink,
) []ai.ToolResultMessage {
	results := make([]ai.ToolResultMessage, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, toolCall *ai.ToolCall) {
			defer wg.Done()
			results[idx] = executeSingleToolCall(ctx, agentCtx, assistantMsg, toolCall, config, emit)
		}(i, tc)
	}
	wg.Wait()
	return results
}

func executeSingleToolCall(
	ctx context.Context,
	agentCtx *AgentContext,
	assistantMsg *ai.AssistantMessage,
	tc *ai.ToolCall,
	config *AgentLoopConfig,
	emit AgentEventSink,
) ai.ToolResultMessage {
	emit(AgentEvent{Type: ToolExecEventStart, ToolCallID: tc.ID, ToolName: tc.Name, Args: tc.Arguments})

	// Find the tool
	var tool *AgentTool
	for i := range agentCtx.Tools {
		if agentCtx.Tools[i].Name == tc.Name {
			tool = &agentCtx.Tools[i]
			break
		}
	}

	if tool == nil {
		return emitToolError(tc, fmt.Sprintf("Tool %s not found", tc.Name), emit)
	}

	// Before hook
	if config.BeforeToolCall != nil {
		beforeResult := config.BeforeToolCall(ctx, BeforeToolCallContext{
			AssistantMessage: assistantMsg,
			ToolCall:         tc,
			Args:             tc.Arguments,
			Context:          agentCtx,
		})
		if beforeResult != nil && beforeResult.Block {
			reason := beforeResult.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			return emitToolError(tc, reason, emit)
		}
	}

	// Execute
	result, err := tool.Execute(ctx, tc.ID, tc.Arguments, func(partial AgentToolResult) {
		emit(AgentEvent{Type: ToolExecEventUpdate, ToolCallID: tc.ID, ToolName: tc.Name, Args: tc.Arguments, Result: &partial})
	})

	isError := err != nil
	if err != nil {
		result = AgentToolResult{
			Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: err.Error()}}},
		}
	}

	// After hook
	if config.AfterToolCall != nil {
		afterResult := config.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: assistantMsg,
			ToolCall:         tc,
			Args:             tc.Arguments,
			Result:           result,
			IsError:          isError,
			Context:          agentCtx,
		})
		if afterResult != nil {
			if afterResult.Content != nil {
				result.Content = afterResult.Content
			}
			if afterResult.Details != nil {
				result.Details = afterResult.Details
			}
			if afterResult.IsError != nil {
				isError = *afterResult.IsError
			}
		}
	}

	boolIsError := isError
	emit(AgentEvent{Type: ToolExecEventEnd, ToolCallID: tc.ID, ToolName: tc.Name, Result: &result, IsError: &boolIsError})

	trm := ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Content:    result.Content,
		Details:    result.Details,
		IsError:    isError,
		Timestamp:  time.Now().UnixMilli(),
	}

	m := ai.Message{ToolResult: &trm}
	emit(AgentEvent{Type: MessageEventStart, Message: &m})
	emit(AgentEvent{Type: MessageEventEnd, Message: &m})

	return trm
}

func emitToolError(tc *ai.ToolCall, message string, emit AgentEventSink) ai.ToolResultMessage {
	isError := true
	result := AgentToolResult{
		Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: message}}},
	}
	emit(AgentEvent{Type: ToolExecEventEnd, ToolCallID: tc.ID, ToolName: tc.Name, Result: &result, IsError: &isError})

	trm := ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Content:    result.Content,
		IsError:    true,
		Timestamp:  time.Now().UnixMilli(),
	}
	m := ai.Message{ToolResult: &trm}
	emit(AgentEvent{Type: MessageEventStart, Message: &m})
	emit(AgentEvent{Type: MessageEventEnd, Message: &m})
	return trm
}
