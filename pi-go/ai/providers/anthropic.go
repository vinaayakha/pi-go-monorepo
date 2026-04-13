package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vinaayakha/pi-go/ai"
)

// AnthropicOptions extends StreamOptions with Anthropic-specific fields.
type AnthropicOptions struct {
	ai.StreamOptions
	ThinkingEnabled      bool   `json:"thinkingEnabled,omitempty"`
	ThinkingBudgetTokens int    `json:"thinkingBudgetTokens,omitempty"`
	Effort               string `json:"effort,omitempty"` // "low","medium","high","max"
}

// RegisterAnthropic registers the Anthropic provider in the global registry.
func RegisterAnthropic() {
	ai.RegisterAPIProvider(&ai.APIProvider{
		API:          ai.APIAnthropicMessages,
		Stream:       streamAnthropic,
		StreamSimple: streamSimpleAnthropic,
	})
}

func streamAnthropic(model ai.Model, ctx ai.Context, opts *ai.StreamOptions) *ai.EventStream {
	es := ai.NewEventStream(64)

	go func() {
		defer es.End()
		doAnthropicStream(model, ctx, opts, es)
	}()

	return es
}

func streamSimpleAnthropic(model ai.Model, ctx ai.Context, opts *ai.SimpleStreamOptions) *ai.EventStream {
	sopts := &ai.StreamOptions{}
	if opts != nil {
		sopts = &opts.StreamOptions
	}
	return streamAnthropic(model, ctx, sopts)
}

func doAnthropicStream(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions, es *ai.EventStream) {
	output := &ai.AssistantMessage{
		Role:       "assistant",
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: ai.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}

	apiKey := ""
	if opts != nil {
		apiKey = opts.APIKey
	}
	if apiKey == "" {
		apiKey = ai.GetEnvAPIKey(model.Provider)
	}
	if apiKey == "" {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = fmt.Sprintf("no API key for provider: %s", model.Provider)
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	// Build request payload
	payload := buildAnthropicPayload(model, aiCtx, opts)
	body, _ := json.Marshal(payload)

	ctx := context.Background()
	if opts != nil && opts.Ctx != nil {
		ctx = opts.Ctx
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = err.Error()
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if opts != nil {
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = err.Error()
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	es.Push(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: output})

	// Parse SSE stream
	parseAnthropicSSE(resp.Body, output, es)
}

func buildAnthropicPayload(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions) map[string]any {
	payload := map[string]any{
		"model":      model.ID,
		"max_tokens": model.MaxTokens / 3,
		"stream":     true,
	}

	if opts != nil && opts.MaxTokens != nil {
		payload["max_tokens"] = *opts.MaxTokens
	}

	if aiCtx.SystemPrompt != "" {
		payload["system"] = aiCtx.SystemPrompt
	}

	messages := convertMessagesAnthropic(aiCtx.Messages)
	payload["messages"] = messages

	if len(aiCtx.Tools) > 0 {
		payload["tools"] = convertToolsAnthropic(aiCtx.Tools)
	}

	return payload
}

func convertMessagesAnthropic(msgs []ai.Message) []map[string]any {
	var result []map[string]any
	for _, msg := range msgs {
		switch {
		case msg.User != nil:
			content := extractTextFromBlocks(msg.User.Content)
			result = append(result, map[string]any{
				"role":    "user",
				"content": content,
			})
		case msg.Assistant != nil:
			blocks := convertAssistantBlocksAnthropic(msg.Assistant.Content)
			result = append(result, map[string]any{
				"role":    "assistant",
				"content": blocks,
			})
		case msg.ToolResult != nil:
			content := extractTextFromBlocks(msg.ToolResult.Content)
			result = append(result, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": msg.ToolResult.ToolCallID,
					"content":     content,
					"is_error":    msg.ToolResult.IsError,
				}},
			})
		}
	}
	return result
}

func convertAssistantBlocksAnthropic(blocks []ai.ContentBlock) []map[string]any {
	var result []map[string]any
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			result = append(result, map[string]any{
				"type": "text",
				"text": b.Text.Text,
			})
		case b.ToolCall != nil:
			result = append(result, map[string]any{
				"type":  "tool_use",
				"id":    b.ToolCall.ID,
				"name":  b.ToolCall.Name,
				"input": b.ToolCall.Arguments,
			})
		}
	}
	return result
}

func convertToolsAnthropic(tools []ai.Tool) []map[string]any {
	var result []map[string]any
	for _, t := range tools {
		result = append(result, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.Parameters,
		})
	}
	return result
}

func extractTextFromBlocks(blocks []ai.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Text != nil {
			parts = append(parts, b.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// parseAnthropicSSE reads an SSE stream and pushes events.
// This is a simplified implementation — production code should handle
// all Anthropic SSE event types (content_block_start, delta, stop, etc.)
func parseAnthropicSSE(r io.Reader, output *ai.AssistantMessage, es *ai.EventStream) {
	decoder := json.NewDecoder(r)
	// Anthropic streams newline-delimited SSE events prefixed with "data: "
	// For a full implementation, parse "event:" and "data:" lines.
	// This stub reads the full response for non-streaming fallback.

	buf, err := io.ReadAll(r)
	if err != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = err.Error()
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	// Parse SSE lines
	lines := strings.Split(string(buf), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)
		switch eventType {
		case "content_block_start":
			cb, _ := event["content_block"].(map[string]any)
			cbType, _ := cb["type"].(string)
			if cbType == "text" {
				output.Content = append(output.Content, ai.ContentBlock{
					Text: &ai.TextContent{Type: "text", Text: ""},
				})
				es.Push(ai.AssistantMessageEvent{
					Type:         ai.EventTextStart,
					ContentIndex: len(output.Content) - 1,
					Partial:      output,
				})
			} else if cbType == "tool_use" {
				id, _ := cb["id"].(string)
				name, _ := cb["name"].(string)
				output.Content = append(output.Content, ai.ContentBlock{
					ToolCall: &ai.ToolCall{Type: "toolCall", ID: id, Name: name, Arguments: map[string]any{}},
				})
				es.Push(ai.AssistantMessageEvent{
					Type:         ai.EventToolCallStart,
					ContentIndex: len(output.Content) - 1,
					Partial:      output,
				})
			}
		case "content_block_delta":
			idx := int(event["index"].(float64))
			delta, _ := event["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)
			if deltaType == "text_delta" {
				text, _ := delta["text"].(string)
				if idx < len(output.Content) && output.Content[idx].Text != nil {
					output.Content[idx].Text.Text += text
					es.Push(ai.AssistantMessageEvent{
						Type:         ai.EventTextDelta,
						ContentIndex: idx,
						Delta:        text,
						Partial:      output,
					})
				}
			} else if deltaType == "input_json_delta" {
				// Accumulate partial JSON for tool call arguments
				partialJSON, _ := delta["partial_json"].(string)
				if idx < len(output.Content) && output.Content[idx].ToolCall != nil {
					es.Push(ai.AssistantMessageEvent{
						Type:         ai.EventToolCallDelta,
						ContentIndex: idx,
						Delta:        partialJSON,
						Partial:      output,
					})
				}
			}
		case "content_block_stop":
			idx := int(event["index"].(float64))
			if idx < len(output.Content) {
				b := output.Content[idx]
				if b.Text != nil {
					es.Push(ai.AssistantMessageEvent{
						Type:         ai.EventTextEnd,
						ContentIndex: idx,
						Content:      b.Text.Text,
						Partial:      output,
					})
				} else if b.ToolCall != nil {
					es.Push(ai.AssistantMessageEvent{
						Type:         ai.EventToolCallEnd,
						ContentIndex: idx,
						ToolCall:     b.ToolCall,
						Partial:      output,
					})
				}
			}
		case "message_delta":
			d, _ := event["delta"].(map[string]any)
			if sr, ok := d["stop_reason"].(string); ok {
				switch sr {
				case "end_turn":
					output.StopReason = ai.StopReasonStop
				case "max_tokens":
					output.StopReason = ai.StopReasonLength
				case "tool_use":
					output.StopReason = ai.StopReasonToolUse
				}
			}
			if u, ok := event["usage"].(map[string]any); ok {
				if v, ok := u["output_tokens"].(float64); ok {
					output.Usage.Output = int(v)
				}
			}
		case "message_start":
			if msg, ok := event["message"].(map[string]any); ok {
				if u, ok := msg["usage"].(map[string]any); ok {
					if v, ok := u["input_tokens"].(float64); ok {
						output.Usage.Input = int(v)
					}
				}
			}
		}
	}

	// Check for tool calls
	hasToolCalls := false
	for _, b := range output.Content {
		if b.ToolCall != nil {
			hasToolCalls = true
			break
		}
	}
	if hasToolCalls && output.StopReason == ai.StopReasonStop {
		output.StopReason = ai.StopReasonToolUse
	}

	output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite

	if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: output.StopReason, Error: output})
	} else {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: output.StopReason, Message: output})
	}

	_ = decoder // suppress unused
}
