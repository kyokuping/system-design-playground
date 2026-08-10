# Agent Guidelines

Working instructions for coding agents in this repository.

## Engineering Principles

- **Go Idiomatic Adaptation**: Follow the source requirements' domain flow and system
  specs faithfully, but restructure them around Go's own concurrency model and
  conventions — goroutines, channels, context.
- **Go Ecosystem First**: When implementing or integrating infrastructure components
  (key-value stores, message queues, caches), prefer Go ecosystem packages and clients.

## Documentation Scope

- Do not read each `tiny_*` module's `docs/decisions/` or `docs/notes.md` unless the user explicitly asks.
  These are learning notes and decision history; they are not needed for ordinary implementation work.
- Treat every `tiny_*` directory as an independent Go module and project within this repository.
  When working inside one, do not read the repository-root `README.md` unless the user explicitly asks.

## Workflow

Work test-first. For behavior changes, preserve Red/Green history when the user explicitly asks for commits:

1. `test(<scope>): specify <behavior>` — a failing test that states the intended behavior
2. `feat(<scope>): <implement it>` or `fix(<scope>): <fix it>` — the code that makes it pass

The Red commit is the only exception to the pre-commit test requirement. Use `LEFTHOOK=0` only for
that commit, and never push a Red commit without its following Green commit.

Name tests `Test<Subject>_<Behavior>`, e.g. `TestGetShortURL_RejectsInvalidURL`.

## Commits and Branches

- Do not create branches or commits unless the user explicitly asks.
- Conventional Commits: `type(scope): imperative subject`, lowercase, no trailing period.
- Scope is the module directory with underscores replaced by hyphens:
  `tiny_url_shortener` → `tiny-url-shortener`. Omit the scope for repo-wide changes.
- For branch names, remove the `tiny_` prefix and replace underscores with hyphens, then append
  the change type: `tiny_url_shortener` → `url-shortener/feat`.

## Commands

Run a command across every `tiny_*` module from the repository root:

```bash
./scripts/for-each-tiny-project.sh go test -race -shuffle=on ./...
```

Lefthook runs `gofmt` on staged files automatically, unit tests on pre-commit, and
race tests plus `golangci-lint` on pre-push — no need to run those manually before
committing.
