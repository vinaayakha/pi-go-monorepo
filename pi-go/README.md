# pi-go

Go port of the pi agent harness, AI streaming layer, coding-agent tools, and conversation threading.

## Packages

| Package | Source | Description |
|---|---|---|
| `ai/` | `packages/ai` | Types, streaming events, event streams, provider registry |
| `ai/providers/` | `packages/ai/src/providers/` | 5 providers: Anthropic, OpenAI Completions, OpenAI Responses, Google Gemini, Mistral |
| `agent/` | `packages/agent` | Agent loop, tool execution (sequential/parallel), steering/follow-up queues |
| `tools/` | `packages/coding-agent/src/core/tools/` | 7 tools with pluggable operations for remote sandboxes |
| `threads/` | (new) | Conversation persistence with in-memory store |

## Providers

| Provider | API | Env Var |
|---|---|---|
| Anthropic Messages | `/v1/messages` | `ANTHROPIC_API_KEY` |
| OpenAI Chat Completions | `/v1/chat/completions` | `OPENAI_API_KEY` |
| **OpenAI Responses** | `/v1/responses` | `OPENAI_API_KEY` |
| Google Gemini | Generative AI | `GOOGLE_API_KEY` / `GEMINI_API_KEY` |
| Mistral | `/v1/chat/completions` | `MISTRAL_API_KEY` |

The OpenAI Responses API (`/v1/responses`) uses a different wire format than Chat Completions — `input` array with typed items, `instructions` field, `function_call`/`function_call_output` items. Both are supported natively.

## Pluggable Operations (Remote Sandboxes)

Tools don't assume local filesystem. Inject custom operations for SSH, containers, cloud sandboxes:

```go
type RemoteFileOps struct { client *ssh.Client }
func (r *RemoteFileOps) ReadFile(path string) ([]byte, error) { /* SSH read */ }
func (r *RemoteFileOps) WriteFile(path string, content []byte, perm os.FileMode) error { /* SSH write */ }
// ... implement tools.FileOps interface

type RemoteExecOps struct { client *ssh.Client }
func (r *RemoteExecOps) Exec(ctx context.Context, command, cwd string) ([]byte, error) { /* SSH exec */ }

cfg := tools.ToolsConfig{
    Cwd:     "/workspace",
    FileOps: &RemoteFileOps{client: sshClient},
    ExecOps: &RemoteExecOps{client: sshClient},
}
agentTools := tools.AllToolsWithConfig(cfg)
```

## Thread Persistence

```go
store := threads.NewMemoryStore()
a := agent.NewAgent(model)
a.ThreadStore = store

// Create thread
threadID, _ := a.NewThread(map[string]string{"user": "alice"})

// Run conversation
a.Prompt(ctx, "Hello")
a.WaitForIdle()

// Thread is auto-saved after each run
// Later: resume
a.LoadThread(threadID)
a.Prompt(ctx, "Continue where we left off")
```

## Quick Start

```bash
export ANTHROPIC_API_KEY=sk-ant-...  # or OPENAI_API_KEY, GOOGLE_API_KEY, MISTRAL_API_KEY
go run . "List files in the current directory"
```

## Integration with apivault

```go
import (
    "github.com/vinaayakha/pi-go/agent"
    "github.com/vinaayakha/pi-go/ai"
    "github.com/vinaayakha/pi-go/ai/providers"
    "github.com/vinaayakha/pi-go/tools"
)

providers.RegisterBuiltins()

// Use OpenAI Responses API (like apivault's existing openai.go)
model := ai.Model{
    ID: "gpt-4o", API: ai.APIOpenAIResponses,
    Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com",
    MaxTokens: 8192,
}

// Inject remote sandbox operations
cfg := tools.ToolsConfig{
    Cwd:     runtime.WorkspaceDir(projectID),
    FileOps: &DropletFileOps{rt: runtime, projectID: projectID},
    ExecOps: &DropletExecOps{rt: runtime, projectID: projectID},
}

a := agent.NewAgent(model)
a.SetTools(tools.CodingToolsWithConfig(cfg))

// Stream events to WebSocket
a.Subscribe(func(event agent.AgentEvent) {
    hub.Emit("agent_event", projectID, event)
})

a.Prompt(ctx, userMessage)
a.WaitForIdle()
```
