// pi-go test harness. Exercise agent loop, tools, threads, hooks, steering,
// custom tool, streaming events. Persist each conversation turn as JSONL
// (apivault ConversationEvent shape) under sessions/<thread_id>.jsonl.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vinaayakha/pi-go/agent"
	"github.com/vinaayakha/pi-go/ai"
	"github.com/vinaayakha/pi-go/ai/providers"
	"github.com/vinaayakha/pi-go/threads"
	"github.com/vinaayakha/pi-go/tools"
)

type ConversationEvent struct {
	Timestamp  time.Time              `json:"timestamp"`
	Type       string                 `json:"type"`
	Role       string                 `json:"role,omitempty"`
	AgentName  string                 `json:"agent_name,omitempty"`
	Content    string                 `json:"content,omitempty"`
	ToolName   string                 `json:"tool_name,omitempty"`
	ToolInput  map[string]interface{} `json:"tool_input,omitempty"`
	ToolOutput string                 `json:"tool_output,omitempty"`
	MessageID  string                 `json:"message_id,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type JSONLLogger struct {
	mu   sync.Mutex
	dir  string
	tid  string
	file *os.File
}

func NewJSONLLogger(dir string) *JSONLLogger {
	_ = os.MkdirAll(dir, 0o755)
	return &JSONLLogger{dir: dir}
}

func (l *JSONLLogger) SetThread(tid string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
	}
	l.tid = tid
	path := filepath.Join(l.dir, tid+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	return nil
}

func (l *JSONLLogger) Write(ev ConversationEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	ev.Timestamp = time.Now()
	data, _ := json.Marshal(ev)
	l.file.Write(append(data, '\n'))
}

func (l *JSONLLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

// loadDotEnv parses a .env file into os.Environ (no deps).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if os.Getenv(strings.TrimSpace(k)) == "" {
			os.Setenv(strings.TrimSpace(k), v)
		}
	}
}

// getTimeTool: custom tool showcasing streaming onUpdate.
func getTimeTool() agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name:        "get_time",
			Description: "Get the current server time in a given timezone (IANA name).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timezone": map[string]any{
						"type":        "string",
						"description": "IANA timezone, e.g. 'UTC' or 'America/Los_Angeles'",
					},
				},
				"required": []string{"timezone"},
			},
		},
		Label: "get_time",
		Execute: func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			tz, _ := params["timezone"].(string)
			loc, err := time.LoadLocation(tz)
			if err != nil {
				return agent.AgentToolResult{
					Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: "invalid timezone: " + err.Error()}}},
				}, nil
			}
			now := time.Now().In(loc).Format(time.RFC1123)
			return agent.AgentToolResult{
				Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: now}}},
			}, nil
		},
	}
}

func extractText(blocks []ai.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Text != nil {
			sb.WriteString(b.Text.Text)
		}
	}
	return sb.String()
}

func main() {
	loadDotEnv(".env")

	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY missing. Copy .env.example to .env and fill in.")
	}
	modelID := os.Getenv("OPENAI_MODEL")
	if modelID == "" {
		modelID = "gpt-4.1-mini"
	}

	providers.RegisterBuiltins()

	model := ai.Model{
		ID:            modelID,
		Name:          modelID,
		API:           ai.APIOpenAICompletions,
		Provider:      ai.ProviderOpenAI,
		BaseURL:       "https://api.openai.com",
		ContextWindow: 128000,
		MaxTokens:     4096,
		Input:         []string{"text"},
	}

	cwd, _ := os.Getwd()
	workspace := filepath.Join(cwd, "workspace")
	_ = os.MkdirAll(workspace, 0o755)

	logger := NewJSONLLogger(filepath.Join(cwd, "sessions"))
	defer logger.Close()

	store := threads.NewMemoryStore()
	a := agent.NewAgent(model)
	a.ThreadStore = store

	a.SystemPrompt = tools.BuildSystemPrompt(tools.BuildSystemPromptOptions{
		CustomPrompt: "You are a pi-go test agent. Use available tools freely. Workspace: " + workspace,
		Cwd:          workspace,
	})

	allTools := append(tools.CodingTools(workspace), getTimeTool())
	a.SetTools(allTools)

	// Before hook: block `rm -rf`, log every call.
	a.BeforeToolCall = func(ctx context.Context, btc agent.BeforeToolCallContext) *agent.BeforeToolCallResult {
		logger.Write(ConversationEvent{
			Type:      "tool_before",
			ToolName:  btc.ToolCall.Name,
			ToolInput: btc.Args,
		})
		if btc.ToolCall.Name == "bash" {
			if cmd, _ := btc.Args["command"].(string); strings.Contains(cmd, "rm -rf") {
				return &agent.BeforeToolCallResult{Block: true, Reason: "rm -rf blocked by test harness"}
			}
		}
		return nil
	}
	a.AfterToolCall = func(ctx context.Context, atc agent.AfterToolCallContext) *agent.AfterToolCallResult {
		logger.Write(ConversationEvent{
			Type:       "tool_after",
			ToolName:   atc.ToolCall.Name,
			ToolOutput: extractText(atc.Result.Content),
		})
		return nil
	}

	// Subscribe → JSONL + live stdout streaming.
	a.Subscribe(func(ev agent.AgentEvent) {
		switch ev.Type {
		case agent.AgentEventStart:
			logger.Write(ConversationEvent{Type: "agent_start"})
		case agent.AgentEventEnd:
			logger.Write(ConversationEvent{Type: "agent_end"})
			fmt.Println()
		case agent.MessageEventUpdate:
			if ev.AssistantMessageEvent != nil && ev.AssistantMessageEvent.Type == ai.EventTextDelta {
				fmt.Print(ev.AssistantMessageEvent.Delta)
			}
		case agent.MessageEventEnd:
			if ev.Message != nil && ev.Message.Assistant != nil {
				logger.Write(ConversationEvent{
					Type:    "message",
					Role:    "assistant",
					Content: extractText(ev.Message.Assistant.Content),
				})
			}
			if ev.Message != nil && ev.Message.User != nil {
				logger.Write(ConversationEvent{
					Type:    "message",
					Role:    "user",
					Content: extractText(ev.Message.User.Content),
				})
			}
		case agent.ToolExecEventStart:
			fmt.Printf("\n[tool:%s] %v\n", ev.ToolName, ev.Args)
			logger.Write(ConversationEvent{
				Type: "tool_start", ToolName: ev.ToolName, ToolInput: ev.Args, MessageID: ev.ToolCallID,
			})
		case agent.ToolExecEventEnd:
			out := ""
			if ev.Result != nil {
				out = extractText(ev.Result.Content)
			}
			logger.Write(ConversationEvent{
				Type: "tool_end", ToolName: ev.ToolName, ToolOutput: out, MessageID: ev.ToolCallID,
			})
		}
	})

	// Seed a thread.
	tid, err := a.NewThread(map[string]string{"source": "pi-go-test"})
	if err != nil {
		log.Fatal(err)
	}
	if err := logger.SetThread(tid); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("pi-go test harness. thread=%s model=%s\n", tid, modelID)
	fmt.Println("commands: /new  /reset  /abort  /steer <msg>  /followup <msg>  /exit")

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	ctx := context.Background()

	for {
		fmt.Print("\n> ")
		if !sc.Scan() {
			return
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		switch {
		case line == "/exit":
			return
		case line == "/new":
			newID, err := a.NewThread(map[string]string{"source": "pi-go-test"})
			if err != nil {
				fmt.Println("err:", err)
				continue
			}
			logger.SetThread(newID)
			fmt.Println("new thread:", newID)
		case line == "/reset":
			a.Reset()
			fmt.Println("reset")
		case line == "/abort":
			a.Abort()
			fmt.Println("abort signal sent")
		case strings.HasPrefix(line, "/steer "):
			msg := strings.TrimPrefix(line, "/steer ")
			a.Steer(ai.Message{User: &ai.UserMessage{
				Role: "user",
				Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: msg}}},
				Timestamp: time.Now().UnixMilli(),
			}})
			fmt.Println("steered")
		case strings.HasPrefix(line, "/followup "):
			msg := strings.TrimPrefix(line, "/followup ")
			a.FollowUp(ai.Message{User: &ai.UserMessage{
				Role: "user",
				Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: msg}}},
				Timestamp: time.Now().UnixMilli(),
			}})
			fmt.Println("queued")
		default:
			if err := a.Prompt(ctx, line); err != nil {
				fmt.Println("err:", err)
				continue
			}
			a.WaitForIdle()
		}
	}
}
