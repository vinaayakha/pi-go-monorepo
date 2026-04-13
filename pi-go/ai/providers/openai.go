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

// RegisterOpenAICompletions registers the OpenAI Chat Completions provider.
func RegisterOpenAICompletions() {
	ai.RegisterAPIProvider(&ai.APIProvider{
		API:          ai.APIOpenAICompletions,
		Stream:       streamOpenAI,
		StreamSimple: streamSimpleOpenAI,
	})
}

func streamOpenAI(model ai.Model, ctx ai.Context, opts *ai.StreamOptions) *ai.EventStream {
	es := ai.NewEventStream(64)
	go func() {
		defer es.End()
		doOpenAIStream(model, ctx, opts, es)
	}()
	return es
}

func streamSimpleOpenAI(model ai.Model, ctx ai.Context, opts *ai.SimpleStreamOptions) *ai.EventStream {
	sopts := &ai.StreamOptions{}
	if opts != nil {
		sopts = &opts.StreamOptions
	}
	return streamOpenAI(model, ctx, sopts)
}

func doOpenAIStream(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions, es *ai.EventStream) {
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
		baseURL = "https://api.openai.com"
	}

	payload := buildOpenAIPayload(model, aiCtx, opts)
	body, _ := json.Marshal(payload)

	goCtx := context.Background()
	if opts != nil && opts.Ctx != nil {
		goCtx = opts.Ctx
	}

	req, err := http.NewRequestWithContext(goCtx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = err.Error()
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
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
	parseOpenAISSE(resp.Body, output, es)
}

func buildOpenAIPayload(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions) map[string]any {
	payload := map[string]any{
		"model":  model.ID,
		"stream": true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if opts != nil && opts.MaxTokens != nil {
		payload["max_completion_tokens"] = *opts.MaxTokens
	}
	if opts != nil && opts.Temperature != nil {
		payload["temperature"] = *opts.Temperature
	}

	var messages []map[string]any
	if aiCtx.SystemPrompt != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": aiCtx.SystemPrompt,
		})
	}
	for _, msg := range aiCtx.Messages {
		switch {
		case msg.User != nil:
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": extractTextFromBlocks(msg.User.Content),
			})
		case msg.Assistant != nil:
			m := map[string]any{"role": "assistant"}
			var toolCalls []map[string]any
			var textParts []string
			for _, b := range msg.Assistant.Content {
				if b.Text != nil {
					textParts = append(textParts, b.Text.Text)
				}
				if b.ToolCall != nil {
					args, _ := json.Marshal(b.ToolCall.Arguments)
					toolCalls = append(toolCalls, map[string]any{
						"id":   b.ToolCall.ID,
						"type": "function",
						"function": map[string]any{
							"name":      b.ToolCall.Name,
							"arguments": string(args),
						},
					})
				}
			}
			if len(textParts) > 0 {
				m["content"] = strings.Join(textParts, "\n")
			}
			if len(toolCalls) > 0 {
				m["tool_calls"] = toolCalls
			}
			messages = append(messages, m)
		case msg.ToolResult != nil:
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": msg.ToolResult.ToolCallID,
				"content":      extractTextFromBlocks(msg.ToolResult.Content),
			})
		}
	}
	payload["messages"] = messages

	if len(aiCtx.Tools) > 0 {
		var tools []map[string]any
		for _, t := range aiCtx.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
					"strict":      true,
				},
			})
		}
		payload["tools"] = tools
	}

	return payload
}

func parseOpenAISSE(r io.Reader, output *ai.AssistantMessage, es *ai.EventStream) {
	buf, err := io.ReadAll(r)
	if err != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = err.Error()
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	var currentTextIdx = -1

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
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		choices, _ := chunk["choices"].([]any)
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)

			// Text content
			if content, ok := delta["content"].(string); ok && content != "" {
				if currentTextIdx == -1 {
					output.Content = append(output.Content, ai.ContentBlock{
						Text: &ai.TextContent{Type: "text", Text: ""},
					})
					currentTextIdx = len(output.Content) - 1
					es.Push(ai.AssistantMessageEvent{Type: ai.EventTextStart, ContentIndex: currentTextIdx, Partial: output})
				}
				output.Content[currentTextIdx].Text.Text += content
				es.Push(ai.AssistantMessageEvent{Type: ai.EventTextDelta, ContentIndex: currentTextIdx, Delta: content, Partial: output})
			}

			// Tool calls
			if tcs, ok := delta["tool_calls"].([]any); ok {
				for _, tc := range tcs {
					tcMap, _ := tc.(map[string]any)
					idx := int(tcMap["index"].(float64))
					fn, _ := tcMap["function"].(map[string]any)

					// Grow content slice if needed
					for len(output.Content) <= idx+1 {
						output.Content = append(output.Content, ai.ContentBlock{})
					}

					tcIdx := idx + 1 // offset by text block
					if output.Content[tcIdx].ToolCall == nil {
						id, _ := tcMap["id"].(string)
						name, _ := fn["name"].(string)
						output.Content[tcIdx] = ai.ContentBlock{
							ToolCall: &ai.ToolCall{Type: "toolCall", ID: id, Name: name, Arguments: map[string]any{}},
						}
						es.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallStart, ContentIndex: tcIdx, Partial: output})
					}
					if args, ok := fn["arguments"].(string); ok {
						es.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallDelta, ContentIndex: tcIdx, Delta: args, Partial: output})
					}
				}
			}

			// Finish reason
			if fr, ok := choice["finish_reason"].(string); ok {
				switch fr {
				case "stop":
					output.StopReason = ai.StopReasonStop
				case "length":
					output.StopReason = ai.StopReasonLength
				case "tool_calls":
					output.StopReason = ai.StopReasonToolUse
				}
			}
		}

		// Usage
		if u, ok := chunk["usage"].(map[string]any); ok {
			if v, ok := u["prompt_tokens"].(float64); ok {
				output.Usage.Input = int(v)
			}
			if v, ok := u["completion_tokens"].(float64); ok {
				output.Usage.Output = int(v)
			}
			if v, ok := u["total_tokens"].(float64); ok {
				output.Usage.TotalTokens = int(v)
			}
		}
	}

	// Close open blocks
	if currentTextIdx >= 0 {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventTextEnd, ContentIndex: currentTextIdx, Content: output.Content[currentTextIdx].Text.Text, Partial: output})
	}
	for i, b := range output.Content {
		if b.ToolCall != nil {
			es.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallEnd, ContentIndex: i, ToolCall: b.ToolCall, Partial: output})
		}
	}

	if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: output.StopReason, Error: output})
	} else {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: output.StopReason, Message: output})
	}
}
