// Non-interactive pi-go autotest. Runs scripted prompts, verifies core
// features (streaming, tools, threads, hooks, custom tool, sub-agent,
// parallel tool execution, Responses API, thread persistence), saves each
// conversation turn as JSONL under sessions/<thread_id>.jsonl, exits
// non-zero on failure. Intended for CI (see sdk-integration-test.yml).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vinaayakha/pi-go/agent"
	"github.com/vinaayakha/pi-go/ai"
	"github.com/vinaayakha/pi-go/ai/providers"
	"github.com/vinaayakha/pi-go/threads"
	"github.com/vinaayakha/pi-go/tools"
)

// ---------- JSONL session logger ----------

type ConversationEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	Type       string         `json:"type"`
	Role       string         `json:"role,omitempty"`
	Content    string         `json:"content,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolInput  map[string]any `json:"tool_input,omitempty"`
	ToolOutput string         `json:"tool_output,omitempty"`
	MessageID  string         `json:"message_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type jsonlLogger struct {
	mu   sync.Mutex
	dir  string
	file *os.File
}

func newLogger(dir string) *jsonlLogger {
	_ = os.MkdirAll(dir, 0o755)
	return &jsonlLogger{dir: dir}
}

func (l *jsonlLogger) setThread(tid string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
	}
	f, err := os.OpenFile(filepath.Join(l.dir, tid+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	return nil
}

func (l *jsonlLogger) write(ev ConversationEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	ev.Timestamp = time.Now()
	data, _ := json.Marshal(ev)
	l.file.Write(append(data, '\n'))
}

func (l *jsonlLogger) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

// ---------- helpers ----------

func extractText(blocks []ai.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Text != nil {
			sb.WriteString(b.Text.Text)
		}
	}
	return sb.String()
}

// ---------- tools ----------

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
			// Small sleep so parallel calls overlap observably.
			time.Sleep(150 * time.Millisecond)
			now := time.Now().In(loc).Format(time.RFC1123)
			return agent.AgentToolResult{
				Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: now}}},
			}, nil
		},
	}
}

// delegateTool spawns a sub-agent with a constrained toolset and returns its
// final assistant text. Demonstrates agent-as-tool sub-agent pattern.
func delegateTool(parentModel ai.Model, workspace string, subAgentFires *atomic.Int64) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name:        "delegate",
			Description: "Delegate a self-contained subtask to a sub-agent. The sub-agent has get_time and coding tools. Returns its final answer.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "Instructions for the sub-agent.",
					},
				},
				"required": []string{"task"},
			},
		},
		Label: "delegate",
		Execute: func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			task, _ := params["task"].(string)
			subAgentFires.Add(1)

			sub := agent.NewAgent(parentModel)
			sub.SystemPrompt = tools.BuildSystemPrompt(tools.BuildSystemPromptOptions{
				CustomPrompt: "You are a sub-agent. Complete the task concisely and return the result.",
				Cwd:          workspace,
			})
			sub.SetTools(append(tools.CodingTools(workspace), getTimeTool()))

			if err := sub.Prompt(ctx, task); err != nil {
				return agent.AgentToolResult{
					Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: "sub-agent error: " + err.Error()}}},
				}, nil
			}
			sub.WaitForIdle()

			msgs := sub.Messages()
			var last string
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Assistant != nil {
					last = extractText(msgs[i].Assistant.Content)
					if last != "" {
						break
					}
				}
			}
			if last == "" {
				last = "(sub-agent returned no text)"
			}
			return agent.AgentToolResult{
				Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: last}}},
			}, nil
		},
	}
}

// ---------- main ----------

type check struct {
	name     string
	prompt   string
	wantTool string
}

func main() {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fatal("OPENAI_API_KEY missing")
	}
	modelID := os.Getenv("OPENAI_MODEL")
	if modelID == "" {
		modelID = "gpt-4o-mini"
	}

	providers.RegisterBuiltins()

	model := ai.Model{
		ID:            modelID,
		Name:          modelID,
		API:           ai.APIOpenAIResponses,
		Provider:      ai.ProviderOpenAI,
		BaseURL:       "https://api.openai.com",
		ContextWindow: 128000,
		MaxTokens:     4096,
		Input:         []string{"text"},
	}

	cwd, _ := os.Getwd()
	workspace := filepath.Join(cwd, "workspace")
	_ = os.MkdirAll(workspace, 0o755)

	logger := newLogger(filepath.Join(cwd, "sessions"))
	defer logger.close()

	store := threads.NewMemoryStore()
	a := agent.NewAgent(model)
	a.ThreadStore = store
	a.ToolExecution = agent.ToolExecParallel
	a.SystemPrompt = tools.BuildSystemPrompt(tools.BuildSystemPromptOptions{
		CustomPrompt: "You are a pi-go autotest agent. Use tools freely. When asked for multiple timezones, call get_time once per timezone in a single turn so they run in parallel. Workspace: " + workspace,
		Cwd:          workspace,
	})

	var subAgentFires atomic.Int64
	a.SetTools(append(
		append(tools.CodingTools(workspace), getTimeTool()),
		delegateTool(model, workspace, &subAgentFires),
	))

	var (
		toolsFired      sync.Map
		textDeltas      atomic.Int64
		msgEnds         atomic.Int64
		agentEnds       atomic.Int64
		blockedCall     atomic.Int64
		toolStarts      atomic.Int64
		toolEnds        atomic.Int64
		maxConcurrent   atomic.Int64
		liveConcurrency atomic.Int64
		getTimeCalls    atomic.Int64
	)

	a.BeforeToolCall = func(ctx context.Context, btc agent.BeforeToolCallContext) *agent.BeforeToolCallResult {
		logger.write(ConversationEvent{
			Type:      "tool_before",
			ToolName:  btc.ToolCall.Name,
			ToolInput: btc.Args,
		})
		if btc.ToolCall.Name == "bash" {
			if cmd, _ := btc.Args["command"].(string); strings.Contains(cmd, "rm -rf") {
				blockedCall.Add(1)
				return &agent.BeforeToolCallResult{Block: true, Reason: "rm -rf blocked by autotest"}
			}
		}
		return nil
	}
	a.AfterToolCall = func(ctx context.Context, atc agent.AfterToolCallContext) *agent.AfterToolCallResult {
		logger.write(ConversationEvent{
			Type:       "tool_after",
			ToolName:   atc.ToolCall.Name,
			ToolOutput: extractText(atc.Result.Content),
		})
		return nil
	}

	a.Subscribe(func(ev agent.AgentEvent) {
		switch ev.Type {
		case agent.AgentEventStart:
			logger.write(ConversationEvent{Type: "agent_start"})
		case agent.AgentEventEnd:
			agentEnds.Add(1)
			logger.write(ConversationEvent{Type: "agent_end"})
		case agent.MessageEventUpdate:
			if ev.AssistantMessageEvent != nil && ev.AssistantMessageEvent.Type == ai.EventTextDelta {
				textDeltas.Add(1)
			}
		case agent.MessageEventEnd:
			msgEnds.Add(1)
			if ev.Message != nil && ev.Message.Assistant != nil {
				logger.write(ConversationEvent{
					Type:    "message",
					Role:    "assistant",
					Content: extractText(ev.Message.Assistant.Content),
				})
			}
			if ev.Message != nil && ev.Message.User != nil {
				logger.write(ConversationEvent{
					Type:    "message",
					Role:    "user",
					Content: extractText(ev.Message.User.Content),
				})
			}
		case agent.ToolExecEventStart:
			toolsFired.Store(ev.ToolName, true)
			toolStarts.Add(1)
			if ev.ToolName == "get_time" {
				getTimeCalls.Add(1)
			}
			now := liveConcurrency.Add(1)
			for {
				prev := maxConcurrent.Load()
				if now <= prev || maxConcurrent.CompareAndSwap(prev, now) {
					break
				}
			}
			logger.write(ConversationEvent{
				Type: "tool_start", ToolName: ev.ToolName, ToolInput: ev.Args, MessageID: ev.ToolCallID,
			})
		case agent.ToolExecEventEnd:
			toolEnds.Add(1)
			liveConcurrency.Add(-1)
			out := ""
			if ev.Result != nil {
				out = extractText(ev.Result.Content)
			}
			logger.write(ConversationEvent{
				Type: "tool_end", ToolName: ev.ToolName, ToolOutput: out, MessageID: ev.ToolCallID,
			})
		}
	})

	tid, err := a.NewThread(map[string]string{"source": "autotest"})
	if err != nil {
		fatal("NewThread: " + err.Error())
	}
	if err := logger.setThread(tid); err != nil {
		fatal("logger.setThread: " + err.Error())
	}
	fmt.Printf("autotest thread=%s model=%s api=responses\n", tid, modelID)

	checks := []check{
		{name: "plain-text", prompt: "Reply with exactly: pi-go-ok"},
		{name: "custom-tool", prompt: "Use the get_time tool with timezone UTC and tell me the result.", wantTool: "get_time"},
		{name: "coding-tool", prompt: "Create a file hello.txt in the workspace with the text 'hi' using your tools.", wantTool: "write"},
		{name: "parallel-tools", prompt: "I need the current time in UTC, America/Los_Angeles, and Asia/Tokyo. Call get_time three times in a single turn (one per timezone), then give me all three answers.", wantTool: "get_time"},
		{name: "sub-agent", prompt: "Delegate this subtask using the delegate tool: 'get the time in Europe/London and return only that string'. Then quote the sub-agent's answer back to me.", wantTool: "delegate"},
	}

	ctx := context.Background()
	for _, c := range checks {
		fmt.Printf("\n=== %s ===\n", c.name)
		if err := a.Prompt(ctx, c.prompt); err != nil {
			fatal(c.name + ": Prompt: " + err.Error())
		}
		a.WaitForIdle()
		if c.wantTool != "" {
			if _, ok := toolsFired.Load(c.wantTool); !ok {
				fatal(c.name + ": expected tool " + c.wantTool + " to fire")
			}
		}
	}

	// Thread persistence: save, create fresh agent, load, verify message replay.
	if err := a.SaveThread(); err != nil {
		fatal("SaveThread: " + err.Error())
	}
	originalMsgs := len(a.Messages())

	a2 := agent.NewAgent(model)
	a2.ThreadStore = store
	if err := a2.LoadThread(tid); err != nil {
		fatal("LoadThread: " + err.Error())
	}
	loadedMsgs := len(a2.Messages())
	if loadedMsgs != originalMsgs {
		fatal(fmt.Sprintf("thread replay mismatch: loaded=%d want=%d", loadedMsgs, originalMsgs))
	}

	// Final assertions.
	if textDeltas.Load() == 0 {
		fatal("no text deltas — streaming broken")
	}
	if msgEnds.Load() == 0 {
		fatal("no message_end events")
	}
	if agentEnds.Load() < int64(len(checks)) {
		fatal(fmt.Sprintf("agent_end count %d < expected %d", agentEnds.Load(), len(checks)))
	}
	if toolStarts.Load() != toolEnds.Load() {
		fatal(fmt.Sprintf("tool_start=%d != tool_end=%d", toolStarts.Load(), toolEnds.Load()))
	}
	if getTimeCalls.Load() < 4 {
		fatal(fmt.Sprintf("get_time fired %d times, expected >= 4 (1 custom-tool + 3 parallel)", getTimeCalls.Load()))
	}
	if maxConcurrent.Load() < 2 {
		fatal(fmt.Sprintf("max concurrent tool execution = %d, expected >= 2 (parallel path not exercised)", maxConcurrent.Load()))
	}
	if subAgentFires.Load() < 1 {
		fatal("delegate tool never spawned sub-agent")
	}

	sessionPath := filepath.Join(cwd, "sessions", tid+".jsonl")
	fi, _ := os.Stat(sessionPath)
	var size int64
	if fi != nil {
		size = fi.Size()
	}

	fmt.Printf("\nautotest OK\n")
	fmt.Printf("  checks=%d passed\n", len(checks))
	fmt.Printf("  deltas=%d msg_ends=%d agent_ends=%d\n", textDeltas.Load(), msgEnds.Load(), agentEnds.Load())
	fmt.Printf("  tool_starts=%d tool_ends=%d max_concurrent=%d\n", toolStarts.Load(), toolEnds.Load(), maxConcurrent.Load())
	fmt.Printf("  sub_agent_fires=%d blocked_rm_rf=%d\n", subAgentFires.Load(), blockedCall.Load())
	fmt.Printf("  thread=%s messages=%d (replay verified)\n", tid, loadedMsgs)
	fmt.Printf("  session=%s (%d bytes)\n", sessionPath, size)

	// Emit GitHub Actions summary rows if running in CI.
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			defer f.Close()
			fmt.Fprintln(f, "## pi-go SDK autotest")
			fmt.Fprintln(f)
			fmt.Fprintln(f, "| Check | Result |")
			fmt.Fprintln(f, "|---|---|")
			for _, c := range checks {
				fmt.Fprintf(f, "| %s | pass |\n", c.name)
			}
			fmt.Fprintln(f)
			fmt.Fprintln(f, "| Metric | Value |")
			fmt.Fprintln(f, "|---|---|")
			fmt.Fprintf(f, "| Text deltas | %d |\n", textDeltas.Load())
			fmt.Fprintf(f, "| Message ends | %d |\n", msgEnds.Load())
			fmt.Fprintf(f, "| Agent ends | %d |\n", agentEnds.Load())
			fmt.Fprintf(f, "| Tool starts | %d |\n", toolStarts.Load())
			fmt.Fprintf(f, "| Max concurrent tools | %d |\n", maxConcurrent.Load())
			fmt.Fprintf(f, "| Sub-agent spawns | %d |\n", subAgentFires.Load())
			fmt.Fprintf(f, "| Thread messages (replayed) | %d |\n", loadedMsgs)
		}
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "autotest FAIL:", msg)
	os.Exit(1)
}
