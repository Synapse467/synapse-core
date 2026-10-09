# synapse-core

The formats and rules of Synapse, as a small Go library with one dependency (the Stellar SDK) and no configuration.

If you want to **use** Synapse, start with [`synapse-cli`](https://github.com/Synapse467/synapse-cli). If you want to **build on** it, or implement it in another language, this is the repository that defines what a capsule, a license and a usage log are, and how each is checked.

> **[`SPEC.md`](SPEC.md)** is the authoritative specification. The Go code here is its reference implementation, and the test vectors in [`testdata/vectors`](testdata/vectors) are how another implementation proves it agrees.

## What is in it

| Package | What it defines |
|---|---|
| [`canonical`](canonical) | Canonical JSON: the one byte sequence for a value, so a hash computed in any language matches. Integers only, sorted keys, minimal escaping. |
| [`identity`](identity) | A Stellar Ed25519 key pair as an identity, created on first use and stored with `0600` permissions (owner-only on Linux and macOS; on Windows it relies on the permissions of your user profile folder). Domain-separated signing, so a signature made for one purpose is never valid for another. |
| [`capsule`](capsule) | The capsule file: manifest, items (claims, procedures, heuristics, exceptions, cases), citations, evaluation record, policy, signatures. Drafts with a review workflow. Complete offline verification. |
| [`license`](license) | Signed licenses, the pure `Check` function that decides a request and fails closed, signed revocations, signed requests, and a replay guard. |
| [`usage`](usage) | A hash-chained, append-only usage log that stores question hashes only, and batch sealing for on-chain receipts. |
| [`chain`](chain) | Optional Stellar client for the three contracts in [`synapse-contracts`](https://github.com/Synapse467/synapse-contracts). Defaults to the public Testnet deployment; funds new accounts with Friendbot; restores expired data automatically. |
| [`home`](home) | Where Synapse keeps its files: your OS's config folder, or `SYNAPSE_HOME` if set. |

## The model in one page

- **A capsule is a file.** JSON, signed by its owner, versioned, with each version's hash chained to the one before.
- **Everything hashed is canonicalised first**, so hashes and signatures are reproducible in any language.
- **Identity is a Stellar address.** There are no accounts. A key is generated locally and never leaves the machine.
- **A license is a file**, signed by the capsule's owner and checked offline by a pure function. Anything that cannot be positively confirmed is a denial.
- **A usage log is a file** in which each entry commits to the one before it, so tampering shows. It stores a hash of each question, never the question.
- **Stellar is optional evidence**: a public anchor for each capsule version, license grant and revocation, and sealed usage batch. Nothing depends on it.

## Using it

```go
import (
    "github.com/Synapse467/synapse-core/capsule"
    "github.com/Synapse467/synapse-core/identity"
    "github.com/Synapse467/synapse-core/license"
)

id, _, _ := identity.LoadOrCreate(identity.DefaultPath())   // made on first use

c, _ := capsule.Load("tenancy.capsule.json")
if report := capsule.Verify(c, nil); !report.OK {
    log.Fatal(report.Issues)                                  // hash, signatures, structure, citations
}

lic, _ := license.Issue(license.Terms{
    Capsule:  license.CapsuleRef{Owner: c.Manifest.Owner, Slug: c.Manifest.Slug},
    Grantee:  "GBUYER…", Purposes: []string{"research"}, MaxQueries: 100,
}, id)

decision := license.Check(lic, c, nil, license.Request{Grantee: "GBUYER…", Purpose: "research", Now: time.Now()})
// decision.Allowed, decision.Code ("ok", "quota_exhausted", "revoked", …), decision.Reason
```

Most programs want the higher-level [`synapse-engine`](https://github.com/Synapse467/synapse-engine) `synapse` package, which adds retrieval, logging and quotas on top of these.

## Guarantees the tests hold to

- Edits after signing are detected: tests cover altered capsules and licenses, forged citation quotes, and a usage log with entries changed, removed or reordered.
- Canonical JSON reproduces every test vector, whose hashes were computed independently with `sha256sum`.
- A license can only be issued by the capsule's owner, and a license, request or revocation cannot be reused under another signature domain.
- Parsers reject unknown fields and enforce size limits before decoding.
- The identity file is never overwritten, even when it is corrupt.
- The Stellar client's full cycle (anchor, grant, record usage, revoke) is exercised against the live Testnet deployment by an opt-in test.

## Build and test

```bash
go test ./...                                       # offline tests only
SYNAPSE_LIVE_TESTNET=1 go test ./chain -run Live    # also exercise the real Testnet contracts
```

## Stability

The formats carry a version (`synapse.capsule/1` and so on). Changes within a version are additive in documentation only; a change to anything that is hashed or signed is a new version, and readers reject formats they do not know.

## Security

Please report vulnerabilities privately; see [SECURITY.md](SECURITY.md). Do not put keys, tokens or customer data in issues, tests or fixtures.

## License

MIT. See [LICENSE.md](LICENSE.md).
