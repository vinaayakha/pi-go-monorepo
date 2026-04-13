# pi-go-monorepo

LLM-agnostic agent SDK in pure Go + a REPL harness that exercises every feature.

- **`pi-go/`** — the library. Zero external dependencies. Providers: Anthropic, OpenAI (Chat Completions + Responses), Google Gemini, Mistral. Streaming events, pluggable sandbox I/O, conversation threads, tool hooks, steering/follow-up, sub-agents.
- **`pi-go-test/`** — interactive REPL that runs against a real OpenAI key, persists every agent turn as JSONL per thread.

See [`pi-go/docs/integration.md`](pi-go/docs/integration.md) for the full integration guide, [`CLAUDE.md`](CLAUDE.md) for coding-agent rules, and [`AGENTS.md`](AGENTS.md) for architecture and contribution notes.

## First-time setup

```bash
git clone <repo>
cd pi-go-monorepo
make setup       # installs git hooks (core.hooksPath → .githooks/) + writes .setup-complete marker
```

`make setup` is **required once per clone**. It:

1. Points `git config core.hooksPath` at `.githooks/` so the secret-scanner pre-commit hook runs on every commit.
2. Creates a gitignored `.setup-complete` marker so tooling (and coding agents) can tell setup has run.

If you skip this step, commits are not scanned for leaked keys.

## Common commands

```bash
make build           # build library + test harness → bin/pi-go-test
make test            # run library tests
make vet             # go vet both modules
make fmt             # gofmt -w both modules
make tidy            # go mod tidy both modules
make run             # build + launch the REPL harness
make clean           # remove bin/
make check-secrets   # scan entire working tree for leaked API keys
make help            # list all targets
```

**Always use the Makefile** — never run `go build` / `go test` with ad-hoc `-o` flags. See `CLAUDE.md` for the rationale.

## Running the REPL harness

```bash
cp pi-go-test/.env.example pi-go-test/.env   # fill in OPENAI_API_KEY
make run
```

REPL commands: `/new`, `/reset`, `/abort`, `/steer <msg>`, `/followup <msg>`, `/exit`.
Every turn is streamed to stdout and persisted to `pi-go-test/sessions/<thread_id>.jsonl`.

## Repo layout

```
pi-go-monorepo/
├── pi-go/              # The library (module: github.com/vinaayakha/pi-go)
├── pi-go-test/         # Interactive REPL harness
├── .githooks/          # Tracked git hooks (installed by make setup)
├── scripts/            # check-secrets.sh, install-hooks.sh
├── Makefile            # Single source of truth for build/test commands
├── CLAUDE.md           # Rules for coding agents working in this repo
├── AGENTS.md           # Architecture guide + contribution workflow
└── LICENSE             # MIT
```

## License

MIT — see [LICENSE](LICENSE).
