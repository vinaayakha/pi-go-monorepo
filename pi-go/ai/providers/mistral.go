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

// RegisterMistral registers the Mistral Conversations provider.
func RegisterMistral() {
	ai.RegisterAPIProvider(&ai.APIProvider{
		API:          ai.APIMistralConversations,
		Stream:       streamMistral,
		StreamSimple: streamSimpleMistral,
	})
}

func streamMistral(model ai.Model, ctx ai.Context, opts *ai.StreamOptions) *ai.EventStream {
	es := ai.NewEventStream(64)
	go func() {
		defer es.End()
		doMistralStream(model, ctx, opts, es)
	}()
	return es
}

func streamSimpleMistral(model ai.Model, ctx ai.Context, opts *ai.SimpleStreamOptions) *ai.EventStream {
	sopts := &ai.StreamOptions{}
	if opts != nil {
		sopts = &opts.StreamOptions
	}
	return streamMistral(model, ctx, sopts)
}

func doMistralStream(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions, es *ai.EventStream) {
	output := &ai.AssistantMessage{
		Role: "assistant", API: model.API, Provider: model.Provider,
		Model: model.ID, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli(),
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
		baseURL = "https://api.mistral.ai"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"

	// Mistral uses OpenAI-compatible format but without stream_options
	payload := buildMistralPayload(model, aiCtx, opts)
	body, _ := json.Marshal(payload)

	goCtx := context.Background()
	if opts != nil && opts.Ctx != nil {
		goCtx = opts.Ctx
	}

	req, err := http.NewRequestWithContext(goCtx, "POST", endpoint, bytes.NewReader(body))
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
	// Reuse the OpenAI SSE parser — Mistral uses the same streaming format
	parseOpenAISSE(resp.Body, output, es)
}

func buildMistralPayload(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions) map[string]any {
	payload := map[string]any{
		"model":  model.ID,
		"stream": true,
	}
	if opts != nil && opts.MaxTokens != nil {
		payload["max_tokens"] = *opts.MaxTokens
	}
	if opts != nil && opts.Temperature != nil {
		payload["temperature"] = *opts.Temperature
	}

	var messages []map[string]any
	if aiCtx.SystemPrompt != "" {
		messages = append(messages, map[string]any{"role": "system", "content": aiCtx.SystemPrompt})
	}
	for _, msg := range aiCtx.Messages {
		switch {
		case msg.User != nil:
			messages = append(messages, map[string]any{
				"role": "user", "content": extractTextFromBlocks(msg.User.Content),
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
						"id": b.ToolCall.ID, "type": "function",
						"function": map[string]any{"name": b.ToolCall.Name, "arguments": string(args)},
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
				"role": "tool", "tool_call_id": msg.ToolResult.ToolCallID,
				"content": extractTextFromBlocks(msg.ToolResult.Content),
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
					"name": t.Name, "description": t.Description, "parameters": t.Parameters,
				},
			})
		}
		payload["tools"] = tools
	}

	return payload
}
