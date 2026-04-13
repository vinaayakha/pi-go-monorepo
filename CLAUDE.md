# CLAUDE.md — pi-go monorepo

Guidance for Claude Code when working in this repo.

## Layout

```
pi-go-monorepo/
├── pi-go/          # Library: LLM-agnostic agent SDK (zero deps, stdlib only)
│   ├── ai/         # Types, provider registry, event stream
│   │   └── providers/  # Anthropic, OpenAI (Completions + Responses), Google, Mistral
│   ├── agent/      # Stateful Agent: loop, events, queues, hooks, threads wiring
│   ├── tools/      # 7 coding tools (read/write/edit/bash/grep/find/ls) + system prompt builder
│   ├── threads/    # Conversation persistence interface + MemoryStore
│   ├── docs/       # integration.md — authoritative integration guide
│   └── go.mod      # module github.com/vinaayakha/pi-go
└── pi-go-test/     # Test harness: interactive REPL exercising every pi-go feature
    ├── main.go     # OpenAI + tools + hooks + threads + JSONL event logging
    └── go.mod      # replace github.com/vinaayakha/pi-go => ../pi-go
```

## First-run check (do this before anything else)

Before running any build, test, or commit in this repo, **check whether `.setup-complete` exists at the monorepo root**. It's gitignored, so on a fresh clone it will be missing.

- If `.setup-complete` is **missing** → this is a first-time workspace. Run `make setup` immediately. That sets `core.hooksPath → .githooks/`, activates the secret-scanner pre-commit hook, and writes the marker. Do not skip — commits made without setup bypass the scanner.
- If `.setup-complete` **exists** → setup has already run; proceed normally.

One-liner for agents:

```bash
[ -f .setup-complete ] || make setup
```

Run this once at the start of any session before touching build/test/commit commands.

## Hard rules

- **Use the Makefile for all builds and tests.** Never run `go build` / `go test` with ad-hoc `-o` flags or random output names. Use `make build`, `make test`, `make vet`, `make run`, `make clean`. Binaries go to `./bin/` only. If you need a target that doesn't exist, add it to the Makefile rather than shelling out directly. This keeps `bin/` clean and prevents stray binaries polluting the tree.
- **pi-go has zero external dependencies.** Stdlib only. Never add a `require` to `pi-go/go.mod`. Test app may use anything, but prefer stdlib.
- **Module path is `github.com/vinaayakha/pi-go`.** Don't rename without updating every import site (including `pi-go-test` replace directive).
- **No backwards-compat shims.** This is a young library — change the API directly, don't stack deprecated wrappers.
- **Provider registration is explicit.** `providers.RegisterBuiltins()` or individual `RegisterX()` calls. Don't auto-register in `init()`.
- **Tool execution goes through `FileOps`/`ExecOps` when `ToolsConfig` is supplied.** Never call `os.ReadFile` / `exec.Command` directly inside a tool — it breaks remote sandbox support.

## Working on pi-go (the library)

- **Read `pi-go/docs/integration.md` first.** It's the public contract. If you change types or behavior, update that doc in the same change.
- **Event types are a public API.** Adding an `AgentEventType` is fine; renaming/removing breaks downstream subscribers. See `agent/types.go`.
- **The agent loop lives in `agent/loop.go`.** It's the trickiest code in the repo — streaming, tool execution, steering, follow-ups, abort all interleave. Read the whole file before editing; don't patch locally.
- **Providers implement `ai.StreamFunc` and push `ai.AssistantMessageEvent`s onto an `EventStream`.** The four required event shapes are `EventStart`, `EventTextDelta` (or tool delta), `EventDone`, plus text/thinking/tool start/end markers. See `ai/providers/openai.go` as the reference implementation.
- **Message type is a tagged union.** `ai.Message` has `User`, `Assistant`, `ToolResult` — exactly one non-nil. Same for `ContentBlock` (`Text`, `Thinking`, `Image`, `ToolCall`). Always check which field is set before dereferencing.
- **Tests:** `go test ./...` from `pi-go/`. If you add a provider, add a fake-server test that replays a canned SSE stream — don't hit real APIs in CI.

## Working on pi-go-test (the harness)

- It's an interactive REPL, not an automated test suite. Its job is to prove every documented feature works end-to-end against a real provider.
- Session events land in `pi-go-test/sessions/<thread_id>.jsonl` using the apivault `ConversationEvent` shape (timestamp, type, role, content, tool_name, tool_input, tool_output, message_id, metadata).
- When a new pi-go feature ships, wire it into `main.go` so there's a way to exercise it from the REPL. Add a `/command` if it needs user input.
- Workspace for tool calls is `./workspace/` (created on startup). Don't let the agent touch anything outside it — the system prompt pins `cwd` to that dir.

## Running

Always go through the Makefile from the monorepo root:

```bash
make setup        # first-time only: install git hooks (core.hooksPath → .githooks/)
make build        # build library + test harness → bin/pi-go-test
make test         # run library tests
make vet          # go vet both modules
make fmt          # gofmt -w both modules
make tidy         # go mod tidy both modules
make run          # build + launch the REPL harness
make clean        # remove bin/
make check-secrets # scan working tree for leaked keys
```

First-time setup for the harness: `cp pi-go-test/.env.example pi-go-test/.env` and fill in `OPENAI_API_KEY`.

REPL commands: `/new`, `/reset`, `/abort`, `/steer <msg>`, `/followup <msg>`, `/exit`.

## Common pitfalls

- **Forgetting `WaitForIdle()`.** `Prompt()` is non-blocking; it returns immediately and runs the agent in a goroutine. If you don't wait, you'll race the next input.
- **Subscribing after `Prompt()`.** Subscribe before the first `Prompt` call — early events will be missed otherwise.
- **Blocking inside a subscriber.** Subscribers run on the agent's event dispatch goroutine. Heavy work (DB writes, HTTP) should be queued or run in its own goroutine.
- **Tool `onUpdate` from a detached goroutine.** Must be called before the `Execute` function returns — after return, the tool call is finalized.
- **Thread auto-save timing.** Threads are persisted on `agent_end`, not after each turn. A crash mid-run loses the in-flight turn.

## When to ask the user

- Adding a new provider or new top-level package.
- Changing any type in `ai/types.go` or `agent/types.go` (public API).
- Anything that requires a new `go.mod` dependency in `pi-go`.
