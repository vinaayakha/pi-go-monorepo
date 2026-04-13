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

// RegisterOpenAIResponses registers the OpenAI Responses API provider (/v1/responses).
func RegisterOpenAIResponses() {
	ai.RegisterAPIProvider(&ai.APIProvider{
		API:          ai.APIOpenAIResponses,
		Stream:       streamOpenAIResponses,
		StreamSimple: streamSimpleOpenAIResponses,
	})
}

func streamOpenAIResponses(model ai.Model, ctx ai.Context, opts *ai.StreamOptions) *ai.EventStream {
	es := ai.NewEventStream(64)
	go func() {
		defer es.End()
		doOpenAIResponsesCall(model, ctx, opts, es)
	}()
	return es
}

func streamSimpleOpenAIResponses(model ai.Model, ctx ai.Context, opts *ai.SimpleStreamOptions) *ai.EventStream {
	sopts := &ai.StreamOptions{}
	if opts != nil {
		sopts = &opts.StreamOptions
	}
	return streamOpenAIResponses(model, ctx, sopts)
}

// ── Wire types for /v1/responses ────────────────────────────────────

type oaiRespRequest struct {
	Model        string        `json:"model"`
	Instructions string        `json:"instructions,omitempty"`
	Input        []interface{} `json:"input"`
	Tools        []oaiRespTool `json:"tools,omitempty"`
	MaxTokens    int           `json:"max_output_tokens,omitempty"`
	Stream       bool          `json:"stream,omitempty"`
}

type oaiRespInputMessage struct {
	Type    string               `json:"type"` // "message"
	Role    string               `json:"role"`
	Content []oaiRespContentPart `json:"content"`
}

type oaiRespContentPart struct {
	Type string `json:"type"` // "input_text" | "output_text"
	Text string `json:"text"`
}

type oaiRespFunctionCallInput struct {
	Type      string `json:"type"` // "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiRespFunctionCallOutput struct {
	Type   string `json:"type"` // "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type oaiRespTool struct {
	Type        string         `json:"type"` // "function"
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiRespResponse struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Error  *oaiRespError     `json:"error,omitempty"`
	Usage  *oaiRespUsage     `json:"usage,omitempty"`
}

type oaiRespError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type oaiRespUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type oaiRespOutputItem struct {
	Type string `json:"type"` // "message" | "function_call"
}

type oaiRespOutputMessage struct {
	Type    string                  `json:"type"`
	ID      string                  `json:"id"`
	Role    string                  `json:"role"`
	Content []oaiRespOutputContent  `json:"content"`
}

type oaiRespOutputContent struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

type oaiRespOutputFunctionCall struct {
	Type      string `json:"type"` // "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ── Implementation ──────────────────────────────────────────────────

func doOpenAIResponsesCall(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions, es *ai.EventStream) {
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
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/responses"

	// Build request
	payload := buildOpenAIResponsesPayload(model, aiCtx, opts)
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

	respBody, _ := io.ReadAll(resp.Body)
	var oaiResp oaiRespResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = fmt.Sprintf("parse response: %v", err)
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	if oaiResp.Error != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = oaiResp.Error.Message
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	// Parse usage
	if oaiResp.Usage != nil {
		output.Usage.Input = oaiResp.Usage.InputTokens
		output.Usage.Output = oaiResp.Usage.OutputTokens
		output.Usage.TotalTokens = oaiResp.Usage.TotalTokens
	}

	es.Push(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: output})

	// Parse output items
	for _, raw := range oaiResp.Output {
		var item oaiRespOutputItem
		if json.Unmarshal(raw, &item) != nil {
			continue
		}

		switch item.Type {
		case "message":
			var msg oaiRespOutputMessage
			if json.Unmarshal(raw, &msg) != nil {
				continue
			}
			for _, c := range msg.Content {
				if c.Type == "output_text" && c.Text != "" {
					idx := len(output.Content)
					output.Content = append(output.Content, ai.ContentBlock{
						Text: &ai.TextContent{Type: "text", Text: c.Text},
					})
					es.Push(ai.AssistantMessageEvent{Type: ai.EventTextStart, ContentIndex: idx, Partial: output})
					es.Push(ai.AssistantMessageEvent{Type: ai.EventTextDelta, ContentIndex: idx, Delta: c.Text, Partial: output})
					es.Push(ai.AssistantMessageEvent{Type: ai.EventTextEnd, ContentIndex: idx, Content: c.Text, Partial: output})
				}
			}

		case "function_call":
			var fc oaiRespOutputFunctionCall
			if json.Unmarshal(raw, &fc) != nil {
				continue
			}
			var args map[string]any
			_ = json.Unmarshal([]byte(fc.Arguments), &args)
			if args == nil {
				args = map[string]any{}
			}
			tc := &ai.ToolCall{
				Type: "toolCall", ID: fc.CallID, Name: fc.Name, Arguments: args,
			}
			idx := len(output.Content)
			output.Content = append(output.Content, ai.ContentBlock{ToolCall: tc})
			output.StopReason = ai.StopReasonToolUse
			es.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallStart, ContentIndex: idx, Partial: output})
			es.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallEnd, ContentIndex: idx, ToolCall: tc, Partial: output})
		}
	}

	if output.StopReason == ai.StopReasonError || output.StopReason == ai.StopReasonAborted {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: output.StopReason, Error: output})
	} else {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: output.StopReason, Message: output})
	}
}

func buildOpenAIResponsesPayload(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions) oaiRespRequest {
	req := oaiRespRequest{
		Model:        model.ID,
		Instructions: aiCtx.SystemPrompt,
	}
	if opts != nil && opts.MaxTokens != nil {
		req.MaxTokens = *opts.MaxTokens
	} else if model.MaxTokens > 0 {
		req.MaxTokens = model.MaxTokens
	}

	// Convert messages to input items
	for _, msg := range aiCtx.Messages {
		switch {
		case msg.User != nil:
			var parts []oaiRespContentPart
			for _, b := range msg.User.Content {
				if b.Text != nil {
					parts = append(parts, oaiRespContentPart{Type: "input_text", Text: b.Text.Text})
				}
			}
			if len(parts) > 0 {
				req.Input = append(req.Input, oaiRespInputMessage{Type: "message", Role: "user", Content: parts})
			}

		case msg.Assistant != nil:
			var textParts []oaiRespContentPart
			for _, b := range msg.Assistant.Content {
				if b.Text != nil {
					textParts = append(textParts, oaiRespContentPart{Type: "output_text", Text: b.Text.Text})
				}
				if b.ToolCall != nil {
					args, _ := json.Marshal(b.ToolCall.Arguments)
					req.Input = append(req.Input, oaiRespFunctionCallInput{
						Type: "function_call", CallID: b.ToolCall.ID,
						Name: b.ToolCall.Name, Arguments: string(args),
					})
				}
			}
			if len(textParts) > 0 {
				req.Input = append(req.Input, oaiRespInputMessage{
					Type: "message", Role: "assistant", Content: textParts,
				})
			}

		case msg.ToolResult != nil:
			resultText := ""
			for _, b := range msg.ToolResult.Content {
				if b.Text != nil {
					resultText += b.Text.Text
				}
			}
			req.Input = append(req.Input, oaiRespFunctionCallOutput{
				Type: "function_call_output", CallID: msg.ToolResult.ToolCallID, Output: resultText,
			})
		}
	}

	// Convert tools
	for _, t := range aiCtx.Tools {
		req.Tools = append(req.Tools, oaiRespTool{
			Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Parameters,
		})
	}

	return req
}
