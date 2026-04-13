# AGENTS.md — pi-go monorepo

Developer guide for humans and coding agents contributing to pi-go.

## What pi-go is

An LLM-agnostic agent SDK in pure Go. Think "Anthropic Agents SDK / OpenAI Agents SDK, but provider-neutral, zero-dependency, and sandbox-pluggable." It gives you:

- **One agent loop** that works across Anthropic, OpenAI (Chat Completions + Responses), Google Gemini, and Mistral.
- **Streaming events** you can forward to WebSocket/SSE without parsing provider-specific SSE frames.
- **7 built-in coding tools** (read, write, edit, bash, grep, find, ls) with pluggable I/O — swap local filesystem for SSH, Docker, E2B, or a custom HTTP sandbox without touching tool code.
- **Conversation threads** with a `Store` interface (memory store included; bring your own DB).
- **Tool hooks** for approval gates, redaction, logging.
- **Steering & follow-up** for mid-run course correction and post-completion queuing.
- **Sub-agent pattern** for hierarchical task delegation.

Target user: backend engineers building coding agents, autonomous workflows, or chat apps with tool use who don't want to lock into a single vendor.

## Repo layout

```
pi-go-monorepo/
├── pi-go/              # The library
│   ├── ai/
│   │   ├── types.go            # Message, ContentBlock, Model, Tool, API/Provider enums
│   │   ├── stream.go           # EventStream, AssistantMessageEvent
│   │   ├── registry.go         # APIProvider registry, RegisterAPIProvider
│   │   └── providers/
│   │       ├── anthropic.go
│   │       ├── openai.go            # Chat Completions
│   │       ├── openai_responses.go  # /v1/responses
│   │       ├── google.go
│   │       ├── mistral.go
│   │       └── register.go          # RegisterBuiltins()
│   ├── agent/
│   │   ├── agent.go            # Agent struct, Prompt/Steer/FollowUp/Abort/Reset
│   │   ├── loop.go             # The core run loop — streams, tool dispatch, hooks
│   │   └── types.go            # AgentEvent, AgentEventType, AgentTool, hook contexts
│   ├── tools/
│   │   ├── read.go write.go edit.go bash.go grep.go find.go ls.go
│   │   ├── config.go           # ToolsConfig, FileOps, ExecOps interfaces
│   │   ├── prompt.go           # BuildSystemPrompt, LoadProjectContextFiles
│   │   └── constructors.go     # CodingTools, ReadOnlyTools, AllTools, *WithConfig
│   ├── threads/
│   │   ├── store.go            # Store interface, Thread type
│   │   └── memory.go           # In-memory implementation
│   ├── docs/
│   │   └── integration.md      # ← authoritative integration guide; read this first
│   ├── main.go                 # Library smoke demo (delete or replace)
│   └── go.mod
└── pi-go-test/         # Interactive REPL harness
    ├── main.go
    ├── .env.example
    └── go.mod                  # replace github.com/vinaayakha/pi-go => ../pi-go
```

## Architectural principles

1. **Zero dependencies in `pi-go/`.** Stdlib only. Adding a dep is a last resort and needs an explicit reason. This is pi-go's biggest selling point against Python SDKs — don't give it up for convenience.
2. **Provider-neutral types.** `ai.Message` and friends are the lingua franca. Providers translate to/from their wire format internally. If a provider feature doesn't fit (e.g., Anthropic cache control, OpenAI reasoning tokens), add it to the shared types with a provider tag — don't leak provider-specific structs upward.
3. **Streaming-first.** Every provider must stream. No "buffered" mode. If a provider API doesn't stream, fake it: emit start → full-text delta → done.
4. **Tools are pure functions of `(ctx, params)` + side effects via `FileOps`/`ExecOps`.** Never hardcode `os.*` or `exec.*` in a tool. This is what makes remote sandboxes work.
5. **Events, not callbacks, for UI integration.** The agent emits a typed event stream. Application code subscribes. Don't add ad-hoc callbacks for every new UI need — extend `AgentEvent`.
6. **The agent is stateful but single-run.** One `Agent` instance = one conversation. Use `Reset()` to clear, `NewThread()` for a new conversation, `LoadThread()` to resume. Don't share an `Agent` across concurrent requests.
7. **Explicit over magic.** Providers must be registered. Tools must be set. System prompts must be built. No implicit global state beyond the provider registry.

## Data flow (one turn)

```
user.Prompt(ctx, "hello")
  │
  ▼
Agent.Prompt → goroutine → loop.Run
  │
  ├─ emit AgentEventStart
  │
  └─ for each turn:
       ├─ emit TurnEventStart
       ├─ call ai.Stream(model, ctx, opts)  ──▶ provider.Stream()
       │    │                                     │
       │    │                                     └─ push events onto EventStream
       │    ▼
       │   consume events:
       │    ├─ EventStart       → emit MessageEventStart
       │    ├─ EventTextDelta   → emit MessageEventUpdate (with delta)
       │    ├─ EventToolStart   → accumulate partial ToolCall
       │    ├─ EventDone        → emit MessageEventEnd
       │    ▼
       ├─ for each tool call in assistant message:
       │    ├─ BeforeToolCall hook   (may block)
       │    ├─ emit ToolExecEventStart
       │    ├─ tool.Execute(ctx, id, args, onUpdate)
       │    │    └─ onUpdate → emit ToolExecEventUpdate
       │    ├─ AfterToolCall hook    (may rewrite result)
       │    ├─ emit ToolExecEventEnd
       │    └─ append ToolResultMessage to transcript
       │
       ├─ emit TurnEventEnd
       ├─ drain Steer queue → inject as user messages → loop
       └─ no tool calls & no steer → drain FollowUp queue → loop or exit
  │
  ├─ persist thread via ThreadStore.SetMessages
  └─ emit AgentEventEnd
```

## Adding a new provider

1. Create `pi-go/ai/providers/<name>.go` implementing `ai.StreamFunc`.
2. Translate `ai.Context` (messages + tools) to the provider's wire format.
3. Open an HTTP connection, parse streaming chunks, push `ai.AssistantMessageEvent` onto the `EventStream` in order: `EventStart` → one or more delta events → `EventDone`.
4. Handle tool calls: accumulate partial tool-call deltas into a complete `ToolCall` content block before `EventDone`.
5. Add a constant to `ai/types.go` (e.g. `APIFooBar API = "foo-bar"`).
6. Register in `register.go` and optionally in `RegisterBuiltins()`.
7. Add a replay test using `net/http/httptest` that serves a canned SSE transcript. No real API calls in tests.
8. Document in `docs/integration.md` table.

## Adding a new tool

1. Create `pi-go/tools/<name>.go` exporting a constructor `FooTool(cfg ToolsConfig) agent.AgentTool`.
2. Define the JSON schema in `Parameters`. Keep properties flat — LLMs handle them better.
3. Route all I/O through `cfg.FileOps` / `cfg.ExecOps`. If the tool needs a capability neither interface provides, extend the interface (and update every implementation).
4. Add to `constructors.go` bundles (`CodingTools`, `AllTools`, etc.) if appropriate.
5. Add a snippet to the `BuildSystemPrompt` tool-snippets table so it appears in auto-generated system prompts.
6. Test with an in-memory `FileOps` fake — don't touch the real filesystem in tests.

## Adding a new event type

Events are a public contract — downstream subscribers switch on `ev.Type`. Rules:

- **Add, don't rename.** New event types are additive; renames are breaking changes.
- Put the constant in `agent/types.go` alongside the existing ones.
- Add any new fields to `AgentEvent` as optional (`omitempty` in JSON).
- Emit from `loop.go` at the appropriate point. Keep emissions ordered — consumers assume causal order.
- Document in `docs/integration.md` event table.

## Threads / persistence

- `threads.Store` is the extension point for databases. Implement the 7 methods.
- `MemoryStore` is the reference — read it before writing your own.
- The agent calls `SetMessages` (not `AppendMessages`) on `agent_end`. The full transcript is rewritten each time. If you need append-only semantics (audit log), emit from an event subscriber, not the thread store.
- Thread metadata is `map[string]string` — intentionally simple. If you need structured metadata, serialize to JSON and store as a single value.

## First-time setup (per clone)

```bash
make setup
```

This configures `git config core.hooksPath .githooks` so the secret-scanner pre-commit hook runs on every commit, and writes a gitignored `.setup-complete` marker. **This must be run once on every fresh clone** — `core.hooksPath` is local git config, not tracked.

**For coding agents:** check for `.setup-complete` at the monorepo root at the start of every session. If missing, run `make setup` before any build, test, or commit. Shell guard:

```bash
[ -f .setup-complete ] || make setup
```

The marker file is gitignored (`.gitignore` entry: `.setup-complete`), so its absence is a reliable signal that setup hasn't run in this working copy yet.

## Build & test workflow

**All builds and tests go through the monorepo Makefile.** Do not invoke `go build` / `go test` with custom `-o` names from scratch — it produces stray binaries and inconsistent artifacts. Binaries land in `./bin/` only.

```
make build   # pi-go ./... + pi-go-test → bin/pi-go-test
make test    # pi-go ./... tests
make vet     # vet both modules
make fmt     # gofmt -w both modules
make tidy    # go mod tidy both modules
make run     # build + launch REPL
make clean   # rm -rf bin/
```

If you need a new workflow (bench, race, coverage, lint), **add a target to the Makefile** rather than running an ad-hoc command. That's the whole point — one source of truth for build commands.

## Testing strategy

- **Library unit tests** (`pi-go/...`): fast, no network. Use `httptest` servers for providers, fake `FileOps`/`ExecOps` for tools. Run via `make test`.
- **Integration smoke tests**: `pi-go-test` REPL, run by hand with a real API key via `make run`. Not in CI.
- **Never commit API keys.** `.env` is gitignored; only `.env.example` is tracked. `pi-go-test/sessions/` and `pi-go-test/workspace/` are also gitignored.

## Release / versioning

Pre-1.0. Break things freely, but:

- Update `docs/integration.md` in the same commit as any public-API change.
- Note breaking changes in the commit message body.
- Bump the minor version on breaking changes once we tag a first release.

## Non-goals

- **Not a prompt framework.** No template language, no chains, no "runnables." Just an agent loop.
- **Not a RAG library.** Embeddings and vector search are out of scope — build them as custom tools on top.
- **Not a multi-agent orchestration framework.** The sub-agent pattern is a recipe, not a framework. If you need complex orchestration, pi-go is a primitive, not the product.
- **Not a UI.** pi-go emits events; you build the UI. `pi-go-test` is a debug REPL, not a reference UI.
