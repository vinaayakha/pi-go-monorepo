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

// RegisterGoogle registers the Google Generative AI (Gemini) provider.
func RegisterGoogle() {
	ai.RegisterAPIProvider(&ai.APIProvider{
		API:          ai.APIGoogleGenerativeAI,
		Stream:       streamGoogle,
		StreamSimple: streamSimpleGoogle,
	})
}

func streamGoogle(model ai.Model, ctx ai.Context, opts *ai.StreamOptions) *ai.EventStream {
	es := ai.NewEventStream(64)
	go func() {
		defer es.End()
		doGoogleStream(model, ctx, opts, es)
	}()
	return es
}

func streamSimpleGoogle(model ai.Model, ctx ai.Context, opts *ai.SimpleStreamOptions) *ai.EventStream {
	sopts := &ai.StreamOptions{}
	if opts != nil {
		sopts = &opts.StreamOptions
	}
	return streamGoogle(model, ctx, sopts)
}

// ── Google Gemini wire types ────────────────────────────────────────

type geminiRequest struct {
	Contents         []geminiContent         `json:"contents"`
	SystemInstruct   *geminiContent          `json:"systemInstruction,omitempty"`
	Tools            []geminiToolDecl        `json:"tools,omitempty"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func doGoogleStream(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions, es *ai.EventStream) {
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
		baseURL = "https://generativelanguage.googleapis.com"
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		strings.TrimRight(baseURL, "/"), model.ID, apiKey)

	payload := buildGeminiPayload(model, aiCtx, opts)
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
	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		output.StopReason = ai.StopReasonError
		output.ErrorMessage = fmt.Sprintf("parse response: %v", err)
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Error: output})
		return
	}

	if gemResp.UsageMetadata != nil {
		output.Usage.Input = gemResp.UsageMetadata.PromptTokenCount
		output.Usage.Output = gemResp.UsageMetadata.CandidatesTokenCount
		output.Usage.TotalTokens = gemResp.UsageMetadata.TotalTokenCount
	}

	es.Push(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: output})

	if len(gemResp.Candidates) > 0 {
		cand := gemResp.Candidates[0]
		switch cand.FinishReason {
		case "STOP":
			output.StopReason = ai.StopReasonStop
		case "MAX_TOKENS":
			output.StopReason = ai.StopReasonLength
		case "SAFETY", "RECITATION", "OTHER":
			output.StopReason = ai.StopReasonError
			output.ErrorMessage = "blocked: " + cand.FinishReason
		}

		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				idx := len(output.Content)
				output.Content = append(output.Content, ai.ContentBlock{
					Text: &ai.TextContent{Type: "text", Text: part.Text},
				})
				es.Push(ai.AssistantMessageEvent{Type: ai.EventTextStart, ContentIndex: idx, Partial: output})
				es.Push(ai.AssistantMessageEvent{Type: ai.EventTextDelta, ContentIndex: idx, Delta: part.Text, Partial: output})
				es.Push(ai.AssistantMessageEvent{Type: ai.EventTextEnd, ContentIndex: idx, Content: part.Text, Partial: output})
			}
			if part.FunctionCall != nil {
				tc := &ai.ToolCall{
					Type: "toolCall", ID: fmt.Sprintf("fc_%d", time.Now().UnixNano()),
					Name: part.FunctionCall.Name, Arguments: part.FunctionCall.Args,
				}
				idx := len(output.Content)
				output.Content = append(output.Content, ai.ContentBlock{ToolCall: tc})
				output.StopReason = ai.StopReasonToolUse
				es.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallStart, ContentIndex: idx, Partial: output})
				es.Push(ai.AssistantMessageEvent{Type: ai.EventToolCallEnd, ContentIndex: idx, ToolCall: tc, Partial: output})
			}
		}
	}

	if output.StopReason == ai.StopReasonError {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: output.StopReason, Error: output})
	} else {
		es.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: output.StopReason, Message: output})
	}
}

func buildGeminiPayload(model ai.Model, aiCtx ai.Context, opts *ai.StreamOptions) geminiRequest {
	req := geminiRequest{}

	if aiCtx.SystemPrompt != "" {
		req.SystemInstruct = &geminiContent{
			Parts: []geminiPart{{Text: aiCtx.SystemPrompt}},
		}
	}

	for _, msg := range aiCtx.Messages {
		switch {
		case msg.User != nil:
			var parts []geminiPart
			for _, b := range msg.User.Content {
				if b.Text != nil {
					parts = append(parts, geminiPart{Text: b.Text.Text})
				}
			}
			req.Contents = append(req.Contents, geminiContent{Role: "user", Parts: parts})

		case msg.Assistant != nil:
			var parts []geminiPart
			for _, b := range msg.Assistant.Content {
				if b.Text != nil {
					parts = append(parts, geminiPart{Text: b.Text.Text})
				}
				if b.ToolCall != nil {
					parts = append(parts, geminiPart{
						FunctionCall: &geminiFunctionCall{Name: b.ToolCall.Name, Args: b.ToolCall.Arguments},
					})
				}
			}
			req.Contents = append(req.Contents, geminiContent{Role: "model", Parts: parts})

		case msg.ToolResult != nil:
			resultText := ""
			for _, b := range msg.ToolResult.Content {
				if b.Text != nil {
					resultText += b.Text.Text
				}
			}
			req.Contents = append(req.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     msg.ToolResult.ToolName,
						Response: map[string]any{"result": resultText},
					},
				}},
			})
		}
	}

	if len(aiCtx.Tools) > 0 {
		var decls []geminiFuncDecl
		for _, t := range aiCtx.Tools {
			decls = append(decls, geminiFuncDecl{
				Name: t.Name, Description: t.Description, Parameters: t.Parameters,
			})
		}
		req.Tools = []geminiToolDecl{{FunctionDeclarations: decls}}
	}

	genConfig := &geminiGenerationConfig{}
	if opts != nil && opts.Temperature != nil {
		genConfig.Temperature = opts.Temperature
	}
	if opts != nil && opts.MaxTokens != nil {
		genConfig.MaxOutputTokens = opts.MaxTokens
	}
	req.GenerationConfig = genConfig

	return req
}
