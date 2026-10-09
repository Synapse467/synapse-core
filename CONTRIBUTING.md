# Contributing

Thank you for helping. A few things keep Synapse trustworthy.

## Before you start

- Read `AGENTS.md` (the rules every change follows) and `docs/REPOSITORIES.md` (which repository owns what).
- Open an issue first for anything that changes a format, a hashing or signing rule, or a licensing decision. Those are specification changes and need discussion. Changing what is hashed or signed means a new format version, never an edit in place.

## Build and test

Go 1.26 or later.

```bash
go vet ./...
go test ./...
SYNAPSE_LIVE_TESTNET=1 go test ./chain -run Live   # optional: talks to the public Testnet
```

## What a good change has

- Tests: meaningful ones, for the supported cases and for malformed or hostile input.
- `SPEC.md` and the documentation updated in the same change, if behaviour changed. If you change canonicalisation, add a test vector that was computed independently of the Go code.
- No secrets, no real customer data, no real documents in fixtures. Use made-up examples.
- Formatted code (`gofmt`) and no `go vet` warnings.
- A note in `CHANGELOG.md` for anything a user would notice.

## Commit messages

Describe what changed and why in the first line (imperative mood), then details if needed.
