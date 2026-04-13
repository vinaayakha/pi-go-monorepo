# pi-go Integration Guide

This document explains how to integrate pi-go into a downstream Go application. pi-go provides an LLM-agnostic agent framework with tool execution, streaming events, pluggable I/O for remote sandboxes, and conversation persistence.

---

## Table of Contents

1. [Architecture overview](#architecture-overview)
2. [Installation](#installation)
3. [Choosing a provider](#choosing-a-provider)
4. [Basic agent usage](#basic-agent-usage)
5. [Pluggable operations (remote sandboxes)](#pluggable-operations)
6. [Custom tools](#custom-tools)
7. [Streaming events to WebSocket / SSE](#streaming-events)
8. [Conversation threads](#conversation-threads)
9. [System prompt construction](#system-prompt-construction)
10. [Before/after tool hooks](#tool-hooks)
11. [Steering and follow-up messages](#steering-and-follow-up)
12. [Sub-agent pattern](#sub-agent-pattern)
13. [Custom providers](#custom-providers)
14. [Full integration example (API server)](#full-example)
15. [Type reference](#type-reference)

---

<a id="architecture-overview"></a>
## 1. Architecture overview

```
┌─────────────────────────────────────────────────────────┐
│  Your application (HTTP server, CLI, worker, etc.)      │
│                                                         │
│   ┌──────────┐   ┌──────────┐   ┌──────────────────┐   │
│   │ agent/   │──▶│ ai/      │──▶│ ai/providers/    │   │
│   │ Agent    │   │ Registry │   │ Anthropic        │   │
│   │ Loop     │   │ Stream() │   │ OpenAI Compl.    │   │
│   │ Events   │   │          │   │ OpenAI Responses │   │
│   └────┬─────┘   └──────────┘   │ Google Gemini    │   │
│        │                        │ Mistral          │   │
│   ┌────▼─────┐                  └──────────────────┘   │
│   │ tools/   │   ┌──────────┐                           │
│   │ read     │──▶│ FileOps  │ ← local or remote        │
│   │ write    │   │ ExecOps  │                           │
│   │ edit     │   └──────────┘                           │
│   │ bash     │                                          │
│   │ grep     │   ┌──────────┐                           │
│   │ find     │   │ threads/ │ ← memory, SQLite, custom  │
│   │ ls       │   │ Store    │                           │
│   └──────────┘   └──────────┘                           │
└─────────────────────────────────────────────────────────┘
```

**Packages:**

| Package | Import path | Purpose |
|---|---|---|
| `ai` | `github.com/vinaayakha/pi-go/ai` | Types (`Message`, `Model`, `Tool`, `Context`), event stream, provider registry |
| `ai/providers` | `github.com/vinaayakha/pi-go/ai/providers` | Built-in LLM providers (Anthropic, OpenAI, Google, Mistral) |
| `agent` | `github.com/vinaayakha/pi-go/agent` | Stateful agent with tool execution loop, event system, queues |
| `tools` | `github.com/vinaayakha/pi-go/tools` | 7 coding tools with pluggable I/O, system prompt builder |
| `threads` | `github.com/vinaayakha/pi-go/threads` | Conversation persistence interface and implementations |

---

<a id="installation"></a>
## 2. Installation

```bash
# Add to your go.mod (replace with your actual module path)
go get github.com/vinaayakha/pi-go
```

pi-go has **zero external dependencies** — only the Go standard library.

---

<a id="choosing-a-provider"></a>
## 3. Choosing a provider

Register providers at startup. Only register what you need — unused providers add no overhead.

```go
import "github.com/vinaayakha/pi-go/ai/providers"

// Register all built-in providers
providers.RegisterBuiltins()

// Or register individually
providers.RegisterAnthropic()
providers.RegisterOpenAICompletions()
providers.RegisterOpenAIResponses()
providers.RegisterGoogle()
providers.RegisterMistral()
```

### Provider details

| Provider | API constant | Endpoint | Auth |
|---|---|---|---|
| Anthropic | `ai.APIAnthropicMessages` | `/v1/messages` | `x-api-key` header |
| OpenAI Completions | `ai.APIOpenAICompletions` | `/v1/chat/completions` | `Bearer` token |
| OpenAI Responses | `ai.APIOpenAIResponses` | `/v1/responses` | `Bearer` token |
| Google Gemini | `ai.APIGoogleGenerativeAI` | Generative AI REST | `key` query param |
| Mistral | `ai.APIMistralConversations` | `/v1/chat/completions` | `Bearer` token |

### Defining a model

```go
model := ai.Model{
    ID:            "claude-sonnet-4-20250514",
    Name:          "Claude Sonnet 4",
    API:           ai.APIAnthropicMessages,
    Provider:      ai.ProviderAnthropic,
    BaseURL:       "https://api.anthropic.com",
    Reasoning:     true,
    Input:         []string{"text", "image"},
    Cost:          ai.ModelCost{Input: 3.0, Output: 15.0},
    ContextWindow: 200000,
    MaxTokens:     8192,
}
```

`BaseURL` can be overridden per-model to point at proxies or custom deployments.

### API key resolution

Keys are resolved in order:
1. `opts.APIKey` (passed per-request)
2. `agent.GetAPIKey(provider)` callback
3. Environment variables via `ai.GetEnvAPIKey(provider)`

Environment variable mapping:

| Provider | Env vars |
|---|---|
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |
| `google` | `GOOGLE_API_KEY`, `GEMINI_API_KEY` |
| `mistral` | `MISTRAL_API_KEY` |
| `groq` | `GROQ_API_KEY` |
| `openrouter` | `OPENROUTER_API_KEY` |

---

<a id="basic-agent-usage"></a>
## 4. Basic agent usage

```go
import (
    "context"
    "github.com/vinaayakha/pi-go/agent"
    "github.com/vinaayakha/pi-go/ai"
    "github.com/vinaayakha/pi-go/ai/providers"
    "github.com/vinaayakha/pi-go/tools"
)

func main() {
    providers.RegisterBuiltins()

    model := ai.Model{
        ID: "claude-sonnet-4-20250514", API: ai.APIAnthropicMessages,
        Provider: ai.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
        ContextWindow: 200000, MaxTokens: 8192,
    }

    a := agent.NewAgent(model)
    a.SystemPrompt = "You are a coding assistant."
    a.SetTools(tools.CodingTools("/workspace"))

    // Subscribe to events for real-time output
    a.Subscribe(func(event agent.AgentEvent) {
        // handle events (see section 7)
    })

    // Run — non-blocking, starts a goroutine
    err := a.Prompt(context.Background(), "Read main.go and explain what it does")
    if err != nil {
        panic(err)
    }

    // Block until the agent finishes
    a.WaitForIdle()

    // Access the transcript
    messages := a.Messages()
}
```

### Key Agent methods

| Method | Description |
|---|---|
| `Prompt(ctx, text)` | Start a run from text. Non-blocking. |
| `PromptMessages(ctx, msgs)` | Start a run from pre-built messages. |
| `WaitForIdle()` | Block until the current run finishes. |
| `Abort()` | Cancel the active run. |
| `Subscribe(fn) → unsubscribe` | Listen to agent lifecycle events. |
| `Steer(msg)` | Inject a message after the current turn. |
| `FollowUp(msg)` | Queue a message for after the agent stops. |
| `Reset()` | Clear all state and queues. |
| `Messages()` | Get a copy of the conversation transcript. |
| `SetTools(tools)` | Replace the tool set. |
| `IsStreaming()` | Check if the agent is currently running. |

---

<a id="pluggable-operations"></a>
## 5. Pluggable operations (remote sandboxes)

By default, tools use local filesystem and `os/exec`. For remote environments (SSH, Docker, cloud sandboxes, E2B, DigitalOcean droplets), implement the operation interfaces:

### FileOps interface

```go
type FileOps interface {
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, content []byte, perm os.FileMode) error
    MkdirAll(path string, perm os.FileMode) error
    Stat(path string) (FileInfo, error)
    ReadDir(path string) ([]DirEntry, error)
    Exists(path string) bool
    Access(path string) error
}
```

### ExecOps interface

```go
type ExecOps interface {
    Exec(ctx context.Context, command, cwd string) (output []byte, err error)
}
```

### Example: SSH-based operations

```go
type SSHFileOps struct {
    client *ssh.Client
}

func (s *SSHFileOps) ReadFile(path string) ([]byte, error) {
    session, _ := s.client.NewSession()
    defer session.Close()
    return session.Output("cat " + shellescape(path))
}

func (s *SSHFileOps) WriteFile(path string, content []byte, perm os.FileMode) error {
    session, _ := s.client.NewSession()
    defer session.Close()
    session.Stdin = bytes.NewReader(content)
    return session.Run(fmt.Sprintf("cat > %s", shellescape(path)))
}

// ... implement remaining methods

type SSHExecOps struct {
    client *ssh.Client
}

func (s *SSHExecOps) Exec(ctx context.Context, command, cwd string) ([]byte, error) {
    session, _ := s.client.NewSession()
    defer session.Close()
    return session.CombinedOutput(fmt.Sprintf("cd %s && %s", shellescape(cwd), command))
}
```

### Example: HTTP API-based operations (for cloud runtimes)

```go
type CloudFileOps struct {
    baseURL   string
    projectID string
    client    *http.Client
}

func (c *CloudFileOps) ReadFile(path string) ([]byte, error) {
    resp, err := c.client.Get(fmt.Sprintf("%s/projects/%s/files?path=%s",
        c.baseURL, c.projectID, url.QueryEscape(path)))
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    return io.ReadAll(resp.Body)
}

// ... implement remaining methods
```

### Wiring it up

```go
cfg := tools.ToolsConfig{
    Cwd:     "/workspace/project-123",
    FileOps: &SSHFileOps{client: sshClient},
    ExecOps: &SSHExecOps{client: sshClient},
}

// All tools now execute against the remote sandbox
agentTools := tools.AllToolsWithConfig(cfg)
a.SetTools(agentTools)
```

### Tool constructors

| Constructor | Env | Description |
|---|---|---|
| `tools.CodingTools(cwd)` | Local | read, bash, edit, write |
| `tools.ReadOnlyTools(cwd)` | Local | read, grep, find, ls |
| `tools.AllTools(cwd)` | Local | All 7 tools |
| `tools.CodingToolsWithConfig(cfg)` | Any | read, bash, edit, write via pluggable ops |
| `tools.AllToolsWithConfig(cfg)` | Any | All 7 tools via pluggable ops |
| `tools.ReadToolWithConfig(cfg)` | Any | Single tool with pluggable ops |
| `tools.WriteToolWithConfig(cfg)` | Any | Single tool with pluggable ops |
| `tools.EditToolWithConfig(cfg)` | Any | Single tool with pluggable ops |
| `tools.BashToolWithConfig(cfg)` | Any | Single tool with pluggable ops |
| `tools.LsToolWithConfig(cfg)` | Any | Single tool with pluggable ops |

---

<a id="custom-tools"></a>
## 6. Custom tools

Define tools as `agent.AgentTool`:

```go
searchKnowledge := agent.AgentTool{
    Tool: ai.Tool{
        Name:        "search_knowledge",
        Description: "Search the project knowledge base for relevant information.",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "query": map[string]any{
                    "type":        "string",
                    "description": "Search query",
                },
            },
            "required": []string{"query"},
        },
    },
    Label: "search_knowledge",
    Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
        query, _ := params["query"].(string)
        results := myKnowledgeStore.Search(query)
        return agent.AgentToolResult{
            Content: []ai.ContentBlock{
                {Text: &ai.TextContent{Type: "text", Text: results}},
            },
        }, nil
    },
}

// Combine with built-in tools
allTools := append(tools.CodingTools("/workspace"), searchKnowledge)
a.SetTools(allTools)
```

### Streaming partial results from tools

Use the `onUpdate` callback to stream progress:

```go
Execute: func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
    for i := 0; i < steps; i++ {
        // Do work...
        onUpdate(agent.AgentToolResult{
            Content: []ai.ContentBlock{
                {Text: &ai.TextContent{Type: "text", Text: fmt.Sprintf("Step %d/%d complete", i+1, steps)}},
            },
        })
    }
    return finalResult, nil
}
```

---

<a id="streaming-events"></a>
## 7. Streaming events to WebSocket / SSE

The agent emits structured events. Subscribe and forward them to your transport layer.

### Event types

| Event | When | Key fields |
|---|---|---|
| `agent_start` | Run begins | — |
| `agent_end` | Run finishes | `Messages` (all new messages) |
| `turn_start` | New LLM turn begins | — |
| `turn_end` | LLM turn + tool results done | `TurnMessage`, `ToolResults` |
| `message_start` | Message streaming begins | `Message` |
| `message_update` | Text/tool delta | `Message`, `AssistantMessageEvent` |
| `message_end` | Message complete | `Message` |
| `tool_execution_start` | Tool begins executing | `ToolCallID`, `ToolName`, `Args` |
| `tool_execution_update` | Tool partial result | `ToolCallID`, `Result` |
| `tool_execution_end` | Tool done | `ToolCallID`, `Result`, `IsError` |

### WebSocket example

```go
a.Subscribe(func(event agent.AgentEvent) {
    // Serialize to JSON
    data, _ := json.Marshal(event)

    // Forward to WebSocket
    hub.Broadcast(projectID, data)
})
```

### SSE example

```go
a.Subscribe(func(event agent.AgentEvent) {
    data, _ := json.Marshal(event)
    fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
    flusher.Flush()
})
```

### Extracting text deltas for streaming display

```go
a.Subscribe(func(event agent.AgentEvent) {
    if event.Type == agent.MessageEventUpdate &&
       event.AssistantMessageEvent != nil &&
       event.AssistantMessageEvent.Type == ai.EventTextDelta {
        // This is a text chunk — send to client
        sendChunk(event.AssistantMessageEvent.Delta)
    }
})
```

---

<a id="conversation-threads"></a>
## 8. Conversation threads

Threads persist conversations across agent runs.

### Using the built-in memory store

```go
import "github.com/vinaayakha/pi-go/threads"

store := threads.NewMemoryStore()

a := agent.NewAgent(model)
a.ThreadStore = store

// Create a thread
threadID, _ := a.NewThread(map[string]string{
    "user_id":    "user-123",
    "project_id": "proj-456",
})

// Run conversation
a.Prompt(ctx, "Hello")
a.WaitForIdle()
// Thread is auto-saved after agent_end

// Later: resume the conversation
a.LoadThread(threadID)
a.Prompt(ctx, "Continue from where we left off")
a.WaitForIdle()
```

### Implementing a custom store (database-backed)

```go
type PostgresThreadStore struct {
    db *sql.DB
}

func (s *PostgresThreadStore) Create(metadata map[string]string) (*threads.Thread, error) {
    id := uuid.New().String()
    metaJSON, _ := json.Marshal(metadata)
    _, err := s.db.Exec(
        "INSERT INTO threads (id, metadata, created_at, updated_at) VALUES ($1, $2, NOW(), NOW())",
        id, metaJSON,
    )
    if err != nil {
        return nil, err
    }
    return &threads.Thread{ID: id, Metadata: metadata, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}

func (s *PostgresThreadStore) Get(id string) (*threads.Thread, error) {
    var metaJSON, msgsJSON []byte
    var createdAt, updatedAt time.Time
    err := s.db.QueryRow(
        "SELECT metadata, messages, created_at, updated_at FROM threads WHERE id = $1", id,
    ).Scan(&metaJSON, &msgsJSON, &createdAt, &updatedAt)
    if err != nil {
        return nil, err
    }
    var meta map[string]string
    var msgs []ai.Message
    json.Unmarshal(metaJSON, &meta)
    json.Unmarshal(msgsJSON, &msgs)
    return &threads.Thread{ID: id, Messages: msgs, Metadata: meta, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func (s *PostgresThreadStore) SetMessages(id string, msgs []ai.Message) error {
    msgsJSON, _ := json.Marshal(msgs)
    _, err := s.db.Exec(
        "UPDATE threads SET messages = $1, updated_at = NOW() WHERE id = $2",
        msgsJSON, id,
    )
    return err
}

// ... implement remaining Store interface methods
```

### Thread store interface

```go
type Store interface {
    Create(metadata map[string]string) (*Thread, error)
    Get(id string) (*Thread, error)
    List() ([]*Thread, error)
    AppendMessages(id string, msgs []ai.Message) error
    SetMessages(id string, msgs []ai.Message) error
    SetMetadata(id string, key, value string) error
    Delete(id string) error
}
```

---

<a id="system-prompt-construction"></a>
## 9. System prompt construction

`tools.BuildSystemPrompt()` assembles a system prompt with tool descriptions, guidelines, and project context (AGENTS.md / CLAUDE.md).

```go
prompt := tools.BuildSystemPrompt(tools.BuildSystemPromptOptions{
    SelectedTools:    []string{"read", "bash", "edit", "write", "search_knowledge"},
    ToolSnippets: map[string]string{
        "read":             "Read file contents",
        "bash":             "Execute shell commands",
        "edit":             "Edit files with exact text replacement",
        "write":            "Create or overwrite files",
        "search_knowledge": "Search the project knowledge base",
    },
    PromptGuidelines: []string{
        "Always search_knowledge before making changes to unfamiliar code.",
        "Run tests after editing code.",
    },
    AppendSystemPrompt: "This project uses Go 1.22 with PostgreSQL.",
    Cwd:                "/workspace/myproject",
    ContextFiles:       tools.LoadProjectContextFiles("/workspace/myproject"),
})

a.SystemPrompt = prompt
```

### Custom system prompt (replaces default)

```go
prompt := tools.BuildSystemPrompt(tools.BuildSystemPromptOptions{
    CustomPrompt: `You are an autonomous agent inside an isolated sandbox.
You have full permission to use all tools. Never ask for confirmation.`,
    Cwd:          "/workspace",
    ContextFiles: contextFiles,
})
```

### Loading AGENTS.md / CLAUDE.md

`tools.LoadProjectContextFiles(cwd)` walks from `cwd` up to the filesystem root, loading the first `AGENTS.md` or `CLAUDE.md` found in each directory. Returns them in root-first order.

---

<a id="tool-hooks"></a>
## 10. Before/after tool hooks

Intercept tool execution for approval, logging, or result modification.

### Before hook (approval gate)

```go
a.BeforeToolCall = func(ctx context.Context, btc agent.BeforeToolCallContext) *agent.BeforeToolCallResult {
    // Block destructive operations
    if btc.ToolCall.Name == "bash" {
        cmd, _ := btc.Args["command"].(string)
        if strings.Contains(cmd, "rm -rf") {
            return &agent.BeforeToolCallResult{
                Block:  true,
                Reason: "Destructive commands are not allowed.",
            }
        }
    }
    // Log all tool calls
    log.Printf("tool: %s args: %v", btc.ToolCall.Name, btc.Args)
    return nil // allow execution
}
```

### After hook (result modification)

```go
a.AfterToolCall = func(ctx context.Context, atc agent.AfterToolCallContext) *agent.AfterToolCallResult {
    // Redact secrets from bash output
    if atc.ToolCall.Name == "bash" {
        for _, block := range atc.Result.Content {
            if block.Text != nil {
                block.Text.Text = redactSecrets(block.Text.Text)
            }
        }
        return &agent.AfterToolCallResult{Content: atc.Result.Content}
    }
    return nil // no modification
}
```

---

<a id="steering-and-follow-up"></a>
## 11. Steering and follow-up messages

### Steering (inject mid-run)

Steering messages are injected after the current LLM turn finishes its tool calls, before the next turn begins.

```go
// While the agent is running, inject a course correction
a.Steer(ai.Message{
    User: &ai.UserMessage{
        Role: "user",
        Content: []ai.ContentBlock{
            {Text: &ai.TextContent{Type: "text", Text: "Focus on the authentication module first."}},
        },
        Timestamp: time.Now().UnixMilli(),
    },
})
```

### Follow-up (queue for after completion)

Follow-up messages run after the agent would otherwise stop (no more tool calls, no steering).

```go
a.FollowUp(ai.Message{
    User: &ai.UserMessage{
        Role: "user",
        Content: []ai.ContentBlock{
            {Text: &ai.TextContent{Type: "text", Text: "Now run the tests."}},
        },
        Timestamp: time.Now().UnixMilli(),
    },
})
```

---

<a id="sub-agent-pattern"></a>
## 12. Sub-agent pattern

Create a sub-agent tool that delegates complex tasks to an isolated agent instance:

```go
func subAgentTool(parentModel ai.Model, cfg tools.ToolsConfig) agent.AgentTool {
    return agent.AgentTool{
        Tool: ai.Tool{
            Name:        "delegate",
            Description: "Delegate a complex subtask to a sub-agent with the same tools.",
            Parameters: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "prompt": map[string]any{"type": "string", "description": "Task for the sub-agent"},
                },
                "required": []string{"prompt"},
            },
        },
        Label: "delegate",
        Execute: func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
            prompt, _ := params["prompt"].(string)

            sub := agent.NewAgent(parentModel)
            sub.SystemPrompt = "You are a focused sub-agent. Complete the task and return results."
            sub.SetTools(tools.CodingToolsWithConfig(cfg))

            err := sub.Prompt(ctx, prompt)
            if err != nil {
                return agent.AgentToolResult{}, err
            }
            sub.WaitForIdle()

            // Extract final assistant text
            var resultText string
            for _, msg := range sub.Messages() {
                if msg.Assistant != nil {
                    for _, b := range msg.Assistant.Content {
                        if b.Text != nil {
                            resultText += b.Text.Text + "\n"
                        }
                    }
                }
            }

            return agent.AgentToolResult{
                Content: []ai.ContentBlock{
                    {Text: &ai.TextContent{Type: "text", Text: resultText}},
                },
            }, nil
        },
    }
}
```

---

<a id="custom-providers"></a>
## 13. Custom providers

Register a custom provider by implementing `ai.StreamFunc`:

```go
func myProvider(model ai.Model, ctx ai.Context, opts *ai.StreamOptions) *ai.EventStream {
    es := ai.NewEventStream(64)
    go func() {
        defer es.End()

        output := &ai.AssistantMessage{
            Role: "assistant", API: model.API, Provider: model.Provider,
            Model: model.ID, StopReason: ai.StopReasonStop,
            Timestamp: time.Now().UnixMilli(),
        }

        // 1. Push start
        es.Push(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: output})

        // 2. Call your API, parse response, push deltas
        text := callMyAPI(model, ctx, opts)
        output.Content = append(output.Content, ai.ContentBlock{
            Text: &ai.TextContent{Type: "text", Text: text},
        })
        es.Push(ai.AssistantMessageEvent{
            Type: ai.EventTextDelta, ContentIndex: 0, Delta: text, Partial: output,
        })

        // 3. Push done
        es.Push(ai.AssistantMessageEvent{
            Type: ai.EventDone, Reason: ai.StopReasonStop, Message: output,
        })
    }()
    return es
}

// Register
ai.RegisterAPIProvider(&ai.APIProvider{
    API:          "my-custom-api",
    Stream:       myProvider,
    StreamSimple: func(m ai.Model, c ai.Context, o *ai.SimpleStreamOptions) *ai.EventStream {
        return myProvider(m, c, &o.StreamOptions)
    },
})
```

---

<a id="full-example"></a>
## 14. Full integration example (API server)

This shows how an application like [apivault](https://github.com/apivault/apivault) would integrate pi-go, replacing its hand-rolled agent loop with the pi-go framework.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"

    "github.com/vinaayakha/pi-go/agent"
    "github.com/vinaayakha/pi-go/ai"
    "github.com/vinaayakha/pi-go/ai/providers"
    "github.com/vinaayakha/pi-go/threads"
    "github.com/vinaayakha/pi-go/tools"
)

// Runtime abstracts your sandbox (droplet, E2B, local, etc.)
type Runtime interface {
    ReadFile(projectID, path string) ([]byte, error)
    WriteFile(projectID, path string, content []byte) error
    Exec(projectID, command string, timeoutMs int) (string, int, error)
    WorkspaceDir(projectID string) string
    // ... other methods
}

// Adapt your runtime to pi-go's interfaces
type runtimeFileOps struct {
    rt        Runtime
    projectID string
}

func (r *runtimeFileOps) ReadFile(path string) ([]byte, error) {
    return r.rt.ReadFile(r.projectID, path)
}
func (r *runtimeFileOps) WriteFile(path string, content []byte, _ os.FileMode) error {
    return r.rt.WriteFile(r.projectID, path, content)
}
func (r *runtimeFileOps) MkdirAll(path string, _ os.FileMode) error {
    _, _, err := r.rt.Exec(r.projectID, "mkdir -p "+path, 5000)
    return err
}
func (r *runtimeFileOps) Stat(path string) (tools.FileInfo, error) {
    out, code, _ := r.rt.Exec(r.projectID,
        fmt.Sprintf(`stat -c '%%n %%s %%F' %s 2>/dev/null`, path), 5000)
    if code != 0 {
        return tools.FileInfo{}, fmt.Errorf("not found")
    }
    // parse output...
    return tools.FileInfo{Name: path, IsDir: false, Size: 0}, nil
}
func (r *runtimeFileOps) ReadDir(path string) ([]tools.DirEntry, error) {
    out, _, _ := r.rt.Exec(r.projectID, "ls -1 "+path, 5000)
    // parse output into DirEntry slice...
    return nil, nil
}
func (r *runtimeFileOps) Exists(path string) bool {
    _, code, _ := r.rt.Exec(r.projectID, "test -e "+path, 3000)
    return code == 0
}
func (r *runtimeFileOps) Access(path string) error {
    _, code, _ := r.rt.Exec(r.projectID, "test -r "+path, 3000)
    if code != 0 {
        return fmt.Errorf("not accessible")
    }
    return nil
}

type runtimeExecOps struct {
    rt        Runtime
    projectID string
}

func (r *runtimeExecOps) Exec(ctx context.Context, command, cwd string) ([]byte, error) {
    fullCmd := fmt.Sprintf("cd %s && %s", cwd, command)
    output, exitCode, err := r.rt.Exec(r.projectID, fullCmd, 120000)
    if err != nil {
        return []byte(output), err
    }
    if exitCode != 0 {
        return []byte(output), fmt.Errorf("exit code %d", exitCode)
    }
    return []byte(output), nil
}

// WebSocket hub for real-time events
type Hub struct { /* ... */ }
func (h *Hub) Emit(event, projectID string, data any) { /* ... */ }

func handleChat(w http.ResponseWriter, r *http.Request, rt Runtime, hub *Hub, store threads.Store) {
    var req struct {
        ProjectID      string `json:"project_id"`
        ConversationID string `json:"conversation_id"`
        Message        string `json:"message"`
        Model          string `json:"model"`
    }
    json.NewDecoder(r.Body).Decode(&req)

    // Register providers once at startup (shown here for clarity)
    providers.RegisterBuiltins()

    // Pick model based on request or config
    model := ai.Model{
        ID: "gpt-4o", API: ai.APIOpenAIResponses,
        Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com",
        MaxTokens: 8192, ContextWindow: 128000,
    }

    // Build tools config pointing at the project's sandbox
    workDir := rt.WorkspaceDir(req.ProjectID)
    cfg := tools.ToolsConfig{
        Cwd:     workDir,
        FileOps: &runtimeFileOps{rt: rt, projectID: req.ProjectID},
        ExecOps: &runtimeExecOps{rt: rt, projectID: req.ProjectID},
    }

    // Build system prompt
    systemPrompt := tools.BuildSystemPrompt(tools.BuildSystemPromptOptions{
        CustomPrompt: "You are an autonomous agent in an isolated sandbox...",
        Cwd:          workDir,
    })

    // Create agent
    a := agent.NewAgent(model)
    a.SystemPrompt = systemPrompt
    a.ThreadStore = store
    a.SetTools(tools.CodingToolsWithConfig(cfg))

    // Add custom tools
    a.SetTools(append(a.Tools(),
        searchKnowledgeTool(req.ProjectID),
        startServerTool(rt, req.ProjectID),
    ))

    // Stream events to WebSocket
    a.Subscribe(func(event agent.AgentEvent) {
        hub.Emit("agent_event", req.ProjectID, event)
    })

    // Load or create thread
    if req.ConversationID != "" {
        a.LoadThread(req.ConversationID)
    } else {
        threadID, _ := a.NewThread(map[string]string{
            "project_id": req.ProjectID,
        })
        req.ConversationID = threadID
    }

    // Run
    if err := a.Prompt(r.Context(), req.Message); err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    // Don't block the HTTP handler — the agent runs in background
    // Events stream via WebSocket
    json.NewEncoder(w).Encode(map[string]string{
        "conversation_id": req.ConversationID,
        "status":          "started",
    })
}
```

---

<a id="type-reference"></a>
## 15. Type reference

### ai.Message

Union type — exactly one field is non-nil:

```go
type Message struct {
    User       *UserMessage
    Assistant  *AssistantMessage
    ToolResult *ToolResultMessage
}
```

### ai.ContentBlock

Union type — exactly one field is non-nil:

```go
type ContentBlock struct {
    Text     *TextContent
    Thinking *ThinkingContent
    Image    *ImageContent
    ToolCall *ToolCall
}
```

### ai.Model

```go
type Model struct {
    ID            string            // e.g. "claude-sonnet-4-20250514"
    Name          string            // Human-readable
    API           API               // e.g. ai.APIAnthropicMessages
    Provider      Provider          // e.g. ai.ProviderAnthropic
    BaseURL       string            // e.g. "https://api.anthropic.com"
    Reasoning     bool              // Supports extended thinking
    Input         []string          // "text", "image"
    Cost          ModelCost         // $/million tokens
    ContextWindow int               // Max context tokens
    MaxTokens     int               // Max output tokens
    Headers       map[string]string // Extra headers per request
}
```

### agent.AgentTool

```go
type AgentTool struct {
    ai.Tool                        // Name, Description, Parameters (JSON Schema)
    Label   string                 // Human-readable label
    Execute func(
        ctx context.Context,
        toolCallID string,
        params map[string]any,
        onUpdate func(AgentToolResult),
    ) (AgentToolResult, error)
}
```

### agent.AgentEvent

```go
type AgentEvent struct {
    Type                  AgentEventType
    Messages              []ai.Message              // agent_end
    TurnMessage           *ai.AssistantMessage       // turn_end
    ToolResults           []ai.ToolResultMessage     // turn_end
    Message               *ai.Message                // message_*
    AssistantMessageEvent *ai.AssistantMessageEvent  // message_update
    ToolCallID            string                     // tool_execution_*
    ToolName              string                     // tool_execution_*
    Args                  map[string]any             // tool_execution_*
    Result                *AgentToolResult           // tool_execution_end
    IsError               *bool                      // tool_execution_end
}
```

### threads.Thread

```go
type Thread struct {
    ID        string
    Messages  []ai.Message
    Metadata  map[string]string
    CreatedAt time.Time
    UpdatedAt time.Time
}
```
