# AGENTS.md

## Project

`ciano` is a CNI plugin written in Go. `SPEC.md` describes the intended
behavior; `README.md` covers usage and the integration harness.

## Commands

- `make build` - build `bin/ciano`
- `make test` - run Go unit tests
- `make vet` - run `go vet`
- `make fmt` - format Go sources with `gofmt`
- `make test-userns` - run the integration harness (needs `unshare`, `ip`,
  `nsenter`, `ping`, and the CNI plugins in `bin/`)
- `make hooks` - install the repo's Git hooks (run once after cloning)

## Commit messages

Every commit subject must start with one of these prefixes:

- `doc:` - documentation changes (README, SPEC, comments, design notes)
- `feat:` - new functionality
- `fix:` - bug fixes and behavioral corrections

Format: `<type>: <lowercase imperative summary>`, no trailing period, ideally
at most 72 characters.

Examples:

- `feat: create veth pair and attach it to the bridge`
- `fix: make DEL idempotent and tolerant of missing state`
- `doc: fix Mermaid sequence diagram rendering`

The `.githooks/commit-msg` hook enforces this. Activate it with `make hooks`.
Do not bypass it with `--no-verify`.
