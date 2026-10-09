# Synapse specification, version 1

This document defines the file formats and rules that make a Synapse capsule, license, revocation, signed request and usage log interoperable. An implementation in any language that follows it will accept exactly what this repository's Go packages accept and refuse what they refuse.

The key words MUST, SHOULD and MAY are used as in RFC 2119.

Contents: [1. Conventions](#1-conventions) · [2. Canonical JSON](#2-canonical-json) · [3. Identity and signatures](#3-identity-and-signatures) · [4. Capsule](#4-capsule) · [5. License](#5-license) · [6. Revocation](#6-revocation) · [7. Signed request](#7-signed-request) · [8. Usage log](#8-usage-log) · [9. On-chain references](#9-on-chain-references) · [10. Size limits](#10-size-limits) · [11. Test vectors](#11-test-vectors)

## 1. Conventions

- Every document is UTF-8 JSON. Files MAY be pretty-printed; only the canonical form (section 2) is hashed.
- A **hash** is the lower-case hexadecimal SHA-256 (64 characters).
- A **address** is a Stellar account address (`G…`, Ed25519). It is the only form of identity: there are no accounts, usernames or passwords.
- A **time** is an RFC 3339 timestamp. Producers SHOULD write UTC.
- Readers MUST reject unknown fields in capsules, licenses, revocations and requests. A field that is not in this document is a different format, not an extension.
- Numbers in hashed data are integers only (section 2). Scores are therefore stored in **basis points** (10000 = 100%).

## 2. Canonical JSON

Anything that is hashed or signed is first reduced to **canonical JSON**, so that every implementation computes the same bytes.

1. No whitespace between tokens.
2. Object keys are sorted by their UTF-8 bytes (not by UTF-16 code units).
3. Strings are written as UTF-8. Only these are escaped: `"` as `\"`, `\` as `\\`, and control characters below U+0020 as `\b \f \n \r \t`, or otherwise `\u00xx` with **lower-case** hex. Everything else, including `<`, `>`, `&`, `/` and non-ASCII text, is written as-is.
4. Numbers MUST be integers within ±(2⁵³ − 1), written without exponent, fraction or leading zeros. A document containing a float, or an integer outside that range, MUST be rejected.
5. `true`, `false` and `null` as usual. Arrays keep their order.
6. A string that is not valid UTF-8 MUST be rejected. (The Go reference implementation replaces invalid bytes while decoding, before canonicalising; implementations that decode strictly MUST reject instead.)

`hash(v) = sha256( canonical(v) )`.

## 3. Identity and signatures

An identity is an Ed25519 key pair. Its public key, encoded as a Stellar address, is the identity. It is generated locally on first use and never leaves the machine.

All signatures are **domain separated**: the signed bytes are

```
domain + "\n" + payload
```

where `payload` is stated for each document below. Signatures are encoded in standard base64. The domains are:

| Domain | Signs |
|---|---|
| `synapse.capsule/1` | a capsule |
| `synapse.license/1` | a license |
| `synapse.revocation/1` | a revocation |
| `synapse.request/1` | a signed request |

A signature made for one domain is never valid for another.

## 4. Capsule

A capsule is one JSON file:

```json
{
  "format": "synapse.capsule/1",
  "manifest": { … },
  "hash": "<hash of manifest>",
  "signatures": [ { "signer": "G…", "role": "owner", "signature": "<base64>" } ]
}
```

`hash = hash(manifest)`. Each signature is over the **ASCII bytes of the hash string**, in domain `synapse.capsule/1`.

### 4.1 Manifest

| Field | Type | Rule |
|---|---|---|
| `slug` | string | `[a-z0-9-]`, 1–63 characters, starts alphanumeric |
| `title` | string | 1–200 characters |
| `domain`, `scope` | string | free text describing the field and where it applies |
| `version` | integer | ≥ 1 |
| `previous` | hash | omitted for version 1; required for later versions: the `hash` of version − 1 |
| `owner` | address | the address that publishes and licenses the capsule |
| `contributors` | array | `{address, name?}`; every item's contributor MUST be the owner or listed here |
| `createdAt` | time | |
| `knowledge` | array of items | at least one; item IDs unique |
| `sources` | array | `{id, title, sha256, bytes}`: only the **hash** of each source document, never its content |
| `evaluation` | object | see 4.3 |
| `policy` | object | see 4.4 |

### 4.2 Item

| Field | Rule |
|---|---|
| `id` | `[a-z0-9][a-z0-9._-]{0,79}`. The reference implementation uses `"item-" + first 12 hex of sha256(type + "\n" + title + "\n" + body + "\n" + contributor)`. |
| `type` | `claim`, `procedure`, `heuristic`, `exception` or `case` |
| `title` | 1–200 characters |
| `body` | up to 4000 characters; REQUIRED unless `type` is `procedure` |
| `steps` | for a procedure: 1–50 steps of 1–1000 characters |
| `conditions`, `exceptions`, `tags` | up to 20 entries of 1–1000 characters |
| `appliesTo` | REQUIRED for an `exception`: the item IDs or tags it qualifies |
| `rationale`, `limitations` | optional text |
| `contributor` | address; REQUIRED, so attribution is never lost |
| `authored` | `true` if the expert wrote it directly rather than extracting it from a source |
| `citations` | `{source, start, end, quote, quoteSha256}`; an item MUST have at least one citation unless `authored` is true |

A citation's `start` and `end` are **byte** offsets into the source document, with `0 ≤ start ≤ end`. `quote` is 1–2000 characters and `quoteSha256 = sha256(UTF-8 bytes of quote)`. `source` MUST name an entry of `sources`. Verifiers that hold the source document SHOULD also check that `sha256(document)` equals the recorded source hash and that `document[start:end] == quote`.

### 4.3 Evaluation

A capsule MUST carry an evaluation with `passed: true`. `suiteHash` identifies the exact question set that was run (the hash of the suite document in `synapse-engine`), `cases` is its size, and four scores in basis points: `coverageBp` (questions the capsule should answer that it did), `abstentionBp` (questions it should refuse that it did), `citationValidityBp` and `attributionBp`. The default publishing thresholds are 8500, 9000, 10000 and 10000.

### 4.4 Policy

`open` (boolean) says whether anyone may consult the capsule without a license. `purposes` lists the purposes allowed without a license. `commercial`, `aiTraining` and `derivative` say whether those uses are allowed without a license. A closed capsule (`open: false`) can only be consulted under a license.

### 4.5 Verification

A verifier MUST check all of the following and reject the capsule if any fails (reporting every problem it found is RECOMMENDED):

1. `format` is `synapse.capsule/1`.
2. The manifest is valid (every rule above, including that version 1 has no `previous` and later versions have one).
3. `hash(manifest)` equals `hash`.
4. Every signature is valid, is from the owner or a listed contributor, and no signer signed twice. A signature with role `owner` MUST be from the owner.
5. The owner has signed.
6. If the previous version is available: it has the same owner and slug, its version is one less, and `previous` equals its `hash`.

Contributor co-signatures are optional. They add evidence that a contributor stands behind the items credited to them; they never replace the owner's signature.

## 5. License

```json
{
  "format": "synapse.license/1",
  "terms": { "id": "<32 hex>", "capsule": {…}, "grantor": "G…", "grantee": "G…", "purposes": ["research"],
             "commercial": false, "aiTraining": false, "derivative": false,
             "notBefore": "…", "expiresAt": "…", "maxQueries": 100, "note": "…" },
  "hash": "<hash of terms>",
  "signature": "<base64>"
}
```

`terms.capsule` is `{owner, slug, minVersion?, maxVersion?, hash?}`; `hash`, when present, pins one exact capsule version. `hash = hash(terms)`; the signature is over the ASCII bytes of the hash in domain `synapse.license/1`, made by `terms.grantor`, who MUST equal `terms.capsule.owner`. `notBefore`, `expiresAt`, `maxQueries` (0 or absent = unlimited), `minVersion` and `maxVersion` (0 or absent = any) are optional.

### 5.1 Deciding a request

Given a license, a capsule, the set of known revocations and a request `{grantee, purpose, now, used, commercial, aiTraining, derivative}`, a request is allowed only if **all** of these hold. The first that fails is the reason code:

| Code | Check |
|---|---|
| `license_required` | (only without a license) the capsule is open |
| `bad_license` | the license is well formed and its signature and hash verify |
| `wrong_capsule` | owner and slug match |
| `wrong_version` | version is within `minVersion`/`maxVersion`, and matches `hash` if pinned |
| `revoked` | no valid revocation of this license is known |
| `wrong_grantee` | the asker is `terms.grantee` |
| `not_yet_valid` / `expired` | `notBefore ≤ now < expiresAt` |
| `purpose_not_allowed` | `purpose` is exactly one of `terms.purposes` (case-sensitive; a purpose of `*` allows any) |
| `commercial_not_allowed`, `ai_training_not_allowed`, `derivative_not_allowed` | a declared use that the license does not grant |
| `quota_exhausted` | `used < maxQueries` |

The decision is a pure function and **fails closed**: anything that cannot be positively confirmed is a denial. For an open capsule with no license, the same use checks apply against the capsule's `policy` (code `ok_open`).

`used` counts allowed consultations that were answered. A question the capsule declines to answer (not covered) is not counted.

**Enforcement.** Checking a license on the holder's own machine is cooperative: whoever has the file can ignore the terms. Authoritative enforcement happens in a gateway that holds the capsule (section 7).

## 6. Revocation

```json
{ "format": "synapse.revocation/1", "license": "<terms.id>", "grantor": "G…", "revokedAt": "…", "signature": "<base64>" }
```

The signature is made by the grantor in domain `synapse.revocation/1` over the **ASCII bytes of the lower-case hex of `sha256(ASCII bytes of hash(body))`**, where `body` is the object without `signature`. It takes effect immediately for any party that has it. A revocation is only valid if the grantor equals the license's grantor; a set of revocations ignores invalid ones.

## 7. Signed request

For a gateway that holds the capsule and enforces its licenses:

```json
{ "format": "synapse.request/1", "capsule": "<capsule hash>", "license": "<license hash, or empty>",
  "purpose": "…", "question": "…", "nonce": "<hex>", "at": "<time>", "grantee": "G…", "signature": "<base64>" }
```

The signature is made by `grantee` in domain `synapse.request/1` over the ASCII bytes of `hash(body)`. A gateway MUST:

1. verify the signature;
2. reject a request whose `at` differs from its clock by more than 5 minutes (default);
3. reject a `(grantee, nonce)` pair it has already seen within twice that window;
4. check that `capsule` equals the hash of the capsule it serves, and that `license` equals the hash of the license presented;
5. then decide the request as in 5.1, using the usage log (section 8) for `used`;
6. record the use, and **withhold the answer if the use cannot be recorded**.

The question is signed but only `sha256(question)` is ever logged.

## 8. Usage log

An append-only sequence of events, one JSON object per line. Each event:

```
{ seq, at, license, capsule, version, grantee, purpose, questionSha256, allowed, code, prev, hash }
```

- `seq` starts at 1 and increases by one.
- `license` is the license's `terms.id`, or `"open"`.
- `prev` is the previous event's `hash` (64 zeros for the first).
- `hash = hash({seq, at, license, capsule, version, grantee, purpose, questionSha256, allowed, code, prev})`.

Editing, removing or reordering any event breaks every later `prev`, so tampering is detected by re-reading the log.

### 8.1 Batches

Allowed events of one license can be sealed into a **batch**: `{license, seq, previous, events[], count, periodStart, periodEnd, hash}`, where `events` lists the event hashes, `previous` is the previous batch's hash for that license (64 zeros for the first), `periodStart`/`periodEnd` are the Unix times of the earliest and latest event, and `hash = hash({license, seq, previous, events, periodStart, periodEnd})`. A batch is what is recorded on-chain (section 9).

## 9. On-chain references

The Soroban contracts in `synapse-contracts` take 32-byte references derived as:

```
capsuleRef = sha256("synapse.capsule\n" + owner + "\n" + slug)
licenseRef = sha256("synapse.license\n" + terms.id)
```

An anchor records `hash` of each capsule version; a grant records the license `hash`; a usage receipt records a batch `hash`. Chain data is **evidence, not authority**: a verifier compares what the chain recorded with the file it holds.

## 10. Size limits

Parsers MUST enforce these before decoding, so hostile files cannot exhaust memory: capsule files ≤ 64 MiB; license and revocation files ≤ 1 MiB; gateway request bodies ≤ 1 MiB; usage log lines ≤ 1 MiB.

## 11. Test vectors

[`testdata/vectors/canonical.json`](testdata/vectors/canonical.json) holds inputs, their canonical text and the SHA-256 of that text (computed independently with `sha256sum`), including cases that MUST be rejected. A conforming canonicaliser reproduces all of them.
