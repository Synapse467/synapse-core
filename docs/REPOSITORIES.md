# Repositories

Synapse is four repositories. Each is independently buildable and has one job. The rule that keeps them honest: **a rule lives in exactly one place**, and the others depend on it rather than copy it.

```
                 ┌──────────────────┐
                 │   synapse-cli    │  the `synapse` command
                 └────────┬─────────┘
                          │ uses
                 ┌────────▼─────────┐
                 │  synapse-engine  │  answering, extracting, evaluating,
                 └────────┬─────────┘  gateway, MCP server, `synapse` library
                          │ uses
                 ┌────────▼─────────┐        records to        ┌───────────────────┐
                 │  synapse-core    │ ───────────────────────▶ │ synapse-contracts │
                 └──────────────────┘   (optional, via chain)  └───────────────────┘
                  formats, signatures,                           Soroban, on Stellar
                  license rules, usage log
```

## Which repository owns what

| Concern | Owner | Why there |
|---|---|---|
| Capsule, license, revocation, request and usage formats | **core** | Anything hashed or signed must have one definition. |
| Canonical JSON, signature domains | **core** | Same reason; also published as [`SPEC.md`](https://github.com/Synapse467/synapse-core/blob/main/SPEC.md). |
| Deciding whether a license permits a request | **core** (`license.Check`) | It must be a pure function with one implementation. |
| Identity (key creation and storage) | **core** | The key signs everything. |
| The Stellar client | **core** (`chain`) | It is how formats reach the chain; it carries the deployed contract addresses. |
| On-chain storage rules | **contracts** | The contracts are the only code that runs on-chain. |
| Retrieval, the decision to decline, rendering an answer | **engine** | Behaviour of answers, separate from the formats. |
| Extraction from documents, evaluation | **engine** | Depends on retrieval, and is used when building capsules. |
| The HTTP gateway, the MCP server | **engine** | They are libraries that other programs mount; the CLI only runs them. |
| The `synapse` library (`Open`, `Ask`) | **engine** | The integration surface for other programs. |
| Command-line parsing, prompts, output, the demo | **cli** | Presentation only. A command does nothing that a Go program could not do through the libraries. |

If the CLI needs behaviour that is not in the engine or core, the behaviour is added there first. Nothing in the CLI duplicates a rule.

## Dependencies

```
synapse-cli       →  synapse-engine, synapse-core
synapse-engine    →  synapse-core
synapse-core      →  Stellar Go SDK
synapse-contracts →  Soroban SDK (Rust); nothing from the others
```

There are no cycles. The contracts do not depend on the Go code; the Go code only knows the contracts' addresses and function signatures.

## Naming

The Go module paths are `github.com/Synapse467/synapse-core`, `…/synapse-engine` and `…/synapse-cli`. Earlier versions of the repositories were named for a different design (`synapse-api`, `synapse-ai`, `synapse-web`); the local folders in this workspace still carry those names until the repositories are renamed on GitHub.

| Folder (today) | GitHub repository to use | Go module |
|---|---|---|
| `synapse-contracts` | `synapse-contracts` (unchanged) | n/a (Rust) |
| `synapse-api` | `synapse-core` | `github.com/Synapse467/synapse-core` |
| `synapse-ai` | `synapse-engine` | `github.com/Synapse467/synapse-engine` |
| `synapse-web` | `synapse-cli` | `github.com/Synapse467/synapse-cli` |

## Releasing

Because each repository must build on its own, releases go in dependency order: **core → engine → cli**. For each:

1. Merge the work and tag it (`v0.1.0`).
2. In the next repository, replace the local-development state of `go.mod` with a `require` on the tag just published, run `go mod tidy`, and test with `GOWORK=off go test ./...` to prove it builds on its own.
3. Tag that repository, and continue down the chain.

`go.work` is for development only and is not committed to any repository.

## Rules for contributors

- Do not put credentials, private keys, seeds, tokens or real customer data in any repository. `.gitignore` excludes `.env` files and `identity.json`.
- Treat input data as sensitive. Never log matched values, questions, answers or full documents by default. Usage logs hold hashes.
- Add fixture tests for anything that changes what is hashed, signed or decided, including malformed input.
- Distinguish what is **prevented** (a check that refuses) from what is only **recorded** (evidence after the fact). Say which in documentation.
