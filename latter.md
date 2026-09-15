# latter.md — go-live verification for agents

When the user says **run latter.md**, follow this file exactly. You are checking that Synapse is a real system: no fabricated transcripts, no fake Stellar hashes, no silent demo adapters in production paths, all four repos healthy.

Do not “make it green” by mocking. If something is missing, **fail and say what the human must do** (`user_task.md`).

---

## 0. Ground rules

- Authoritative PRD: `/home/gamp/Downloads/SYNAPSE_MASTER_BUILD.md` (or the copy the user points to).
- Four repos only: `synapse-web`, `synapse-api`, `synapse-ai`, `synapse-contracts`.
- `/demo` on the web **may** exist but must stay an explicitly labelled browser-local adapter. Studio (`/studio`) must talk to the real API.
- Production-like env: `SYNAPSE_ENV=production` (or unset, which defaults to production in transcription). The UTF-8 transcription fixture is **forbidden** unless `SYNAPSE_ENV != production`.
- Never invent Stellar tx hashes, transcripts, OCR text, or “scan clean” when the scanner cannot run.

---

## 1. Prove the code is not mock

Search and **fail** if you find any of these in production paths:

| Forbidden | Where to look |
|---|---|
| Fabricated transcript fallback | `synapse-ai/src/synapse_ai/main.py` `transcribe` — must 503 without provider (except `SYNAPSE_ENV != production` UTF-8 fixture). |
| Fake Stellar hash | `synapse-api/src/stellar.ts` — `invoke` must submit via RPC; no `sha256("fake")` style hashes returned as `txHash`. |
| Silent scope bypass | `evals/generate` / `retrieve` must default missing `allowedScope` keys to **empty** (deny). |
| `tsx` NestJS `dev` | `synapse-api/package.json` `dev` must go through `tsc` (decorator metadata). |
| Demo used as Studio | `synapse-web` `/studio` uses `api("/workspace")`, not `loadDemo()`. |

`/demo` using `loadDemo()` is allowed.

---

## 2. Repo health (must all pass)

```sh
# API
cd synapse-api && pnpm typecheck && pnpm lint && pnpm test && pnpm build

# Web
cd synapse-web && pnpm typecheck && pnpm lint && pnpm test && pnpm build

# AI
cd synapse-ai && source .venv/bin/activate && ruff check . && mypy src && pytest -q

# Contracts
export PATH="$HOME/.cargo/bin:$PATH"
cd synapse-contracts && cargo fmt --check && cargo test --workspace
```

If disk is tight, `cargo clean` after contracts tests.

---

## 3. Live stack

Required processes (or equivalent containers):

1. Postgres + Redis + MinIO (or real S3) from `synapse-api/docker-compose.dev.yml` (local) **or** production equivalents.
2. `synapse-ai` on loopback/private network with `AI_SERVICE_TOKEN` matching the API.
3. `synapse-api` with `WEB_ORIGIN` matching the smoke/web origin.
4. Optional: `synapse-web` `pnpm dev` / production URL.

Health:

```sh
curl -sS http://127.0.0.1:4000/v1/health
curl -sS http://127.0.0.1:8000/internal/v1/health \
  -H "Authorization: Bearer $AI_SERVICE_TOKEN"
```

Both must be 200. AI health without the bearer must be 401.

---

## 4. End-to-end smoke (real DB + real AI)

```sh
cd synapse-api
# WEB_ORIGIN must match the script (http://localhost:3100)
WEB_ORIGIN=http://localhost:3100 pnpm dev   # or pnpm start after build
node test/live-smoke.mjs
```

Must pass: register, source → extract → approve, eval, publish, cited query, abstention, revoke, orgs, MFA, org-owned capsule, credential submit, credential review **rejected for non-reviewer**, redact, `GET /me/licenses`, `GET /capsules/:id/usage`, `GET /capsules/:id/settlements`.

---

## 5. Production-readiness gates (fail closed)

If the user asked for production / “good to go live”:

| Check | Fail if |
|---|---|
| Transcription | `TRANSCRIPTION_API_URL` or `TRANSCRIPTION_API_KEY` empty. |
| `SYNAPSE_ENV` | Not `production`. |
| Stellar signer | `STELLAR_SIGNER_SECRET` missing or invalid. |
| Contract IDs | Any of capsule/license/usage/settlement IDs empty. |
| Token | `AI_SERVICE_TOKEN` shorter than 32 chars or mismatched across API/AI. |
| Origin | `WEB_ORIGIN` is localhost while they claim production. |
| Scanner | `CLAMAV_REQUIRED=true` but `CLAMAV_CMD` missing (this is correct fail-closed). For production uploads, **require** ClamAV installed and `CLAMAV_REQUIRED=true`. |
| Tesseract | `tesseract --version` fails — scanned PDFs will 503. Report it; do not fake OCR. |
| Object storage | Still default `synapse-local` keys on a public host. |
| Demo | Marketing the `/demo` URL as the live product. |

Settlement: priced usage + contributor Stellar public keys + `STELLAR_SETTLEMENT_CONTRACT_ID` + funded signer. Otherwise jobs must error or record `RECORDED_OFFCHAIN` — never a fake tx.

---

## 6. What “live and good to go” means

All of the following are true:

- Four repos: tests green, working trees understood (report uncommitted diffs).
- API + AI healthy with matching service token.
- Smoke path green against real Postgres/Redis/AI.
- No production path fabricates transcripts, citations, or chain hashes.
- Human items in `user_task.md` are either **done** (you verified the env) or **listed as blocking**.

If any human item is still missing, your final sentence is:

> Not production-complete. Blocking human tasks: … (point at `user_task.md`).

If everything above passed **and** the production env gates passed:

> Live stack verified. No mock production paths. Remaining risk is operational (keys, hosting, legal) as listed in `user_task.md`.
