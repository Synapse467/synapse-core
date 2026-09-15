# Synapse API

NestJS 12, Fastify, Prisma 7.10, PostgreSQL, Redis/BullMQ, and private S3-compatible storage. The authoritative PRD remains `../SYNAPSE_MASTER_BUILD.md`.

## Local setup

```sh
pnpm install --frozen-lockfile
docker compose -f docker-compose.dev.yml up -d
pnpm db:generate
pnpm db:migrate
pnpm build
pnpm start
```

`pnpm dev` also works for iterative development (`tsc --watch` + `node --watch`, restarting on every compiled change) and is the recommended loop while editing — it always goes through the real TypeScript compiler rather than a fast transpiler like `tsx`/esbuild, because this codebase relies on NestJS's implicit type-based constructor injection (`design:paramtypes` decorator metadata), which `tsx`/esbuild silently fails to emit; a fast-transpiler-based `dev` script previously crashed on every single start for exactly that reason.

Copy `.env.example` to `.env` and set the matching private AI service token. Local development credentials are not production credentials. The API listens on localhost:4000. Set `WEB_ORIGIN` to the exact web origin. Session cookies are HttpOnly, SameSite=Lax, Secure in production, stored server-side by token hash. Passwords use salted scrypt. Writes require the configured Origin.

API docs: `/v1/docs`. Client projections are in the web repository. The initial SQL migration includes foreign keys, usage-cap checks, and immutable version/audit triggers in addition to Prisma-managed tables; preserve these in future migrations.

Implemented: identity/session endpoints (including Freighter wallet sign-in), workspace projection/actions, ownership-scoped capsule editing (including organization-owned capsules), private source upload/finalization with SHA-256 validation, idempotent source jobs, prompt-injection/XSS security scanning on all source and knowledge text before it is persisted or sent to extraction, approval/rejection/correction, AI-generated and expert-authored evaluation cases, revision-specific publish gate, immutable snapshots/manifests, real Stellar Testnet anchoring for capsule publication and license grant/revocation (background workers, idempotent, with an audited DB trigger fix so anchor write-back can never silently fail), grants/revocation, public discovery, licensed conversations, provenance validation, serializable usage enforcement, ordered recoverable interview chunks with real AI transcription and automatic assembly into a reviewable source once every segment is transcribed, follow-up questions, organizations with membership and TOTP-based admin MFA enforcement, and expert-credential evidence submission/listing/review.

## Organizations & MFA

`POST /v1/organizations` creates an organization; the creator becomes its first `ADMIN` member. Any member may create a capsule under the organization (`workspace/actions` `create-capsule` with `data.organizationId`), and any member can edit it. Admin-only actions — currently just inviting a member — additionally require a **currently valid TOTP code**: `POST /v1/organizations/:id/mfa/enroll` returns a secret + `otpauth://` URI for any standard authenticator app, and `POST /v1/organizations/:id/mfa/verify` activates enforcement. Until an admin enrolls and verifies MFA, admin-mutating actions are rejected outright — MFA is mandatory for org admins per the PRD, not optional. TOTP is implemented from scratch (RFC 6238, HMAC-SHA1) in `src/core.ts` with no new dependency.

## Expert credentials

`POST /v1/experts/me/verification` submits verification evidence (`type`, `issuer`, optional `evidenceObjectKey`) as a `PENDING` `ExpertCredential`. `GET /v1/experts/me/credentials` lists the caller's own submissions. `POST /v1/experts/credentials/:id/review` approves/rejects evidence — **documented deviation**: the PRD does not define a platform-reviewer/moderator role or data model, and this environment has no real identity-verification service to call, so review access is gated by a `PLATFORM_REVIEWER_EMAILS` comma-separated env allowlist rather than a fabricated automatic approval. An approved credential sets `User.verificationStatus = "VERIFIED"`.

## Checks

```sh
pnpm typecheck
pnpm lint
pnpm test
pnpm build
node test/live-smoke.mjs
```

The smoke script creates synthetic accounts and data in the local Synapse database and retains them for inspection. It does not submit blockchain transactions or payments.

## Incomplete product requirements

Do not deploy as production-complete. Still open:
- Credential review uses an env-var reviewer allowlist, not a real platform-moderator role/model (see above).
- Usage-receipt batching (`UsageReceiptRegistry.record`) exists in `stellar.ts`/the contract but nothing schedules/triggers a periodic batcher yet — no product flow calls it.
- Settlement orchestration (payout routing against contributor shares) is not wired into any API flow; the `Settlement` contract's split arithmetic is implemented and tested in isolation only.
- Provider data-retention controls, transcript redaction/deletion workflows, and secure-deletion-on-policy for unpublished sources are not implemented.
- Full PRD endpoint-name aliases and a generated OpenAPI request-schema client are not built; the API is fully typed/validated/documented at `/v1/docs` but consumers hand-write request shapes rather than importing generated types.
- Retrieval is source-grounded extractive matching, not semantic/vector search (PRD explicitly allows deferring pgvector until scale requires it — this is intentional, not a gap).
- Audit coverage is broad (every state-changing action) but not exhaustively enumerated against every PRD data field.
- Idempotency keys are enforced on every job and on financially/access-relevant mutations (messages, uploads, Stellar jobs); a few low-risk read-adjacent actions do not require one.

MinIO uses its documented Quay registry because the Docker Hub image was unavailable. Reference: https://min.io/docs/minio/container/operations/install-deploy-manage/deploy-minio-single-node-multi-drive.html

Verified 2026-09-15: the full `test/live-smoke.mjs` end-to-end suite passes against a real Postgres, Redis, and a live `synapse-ai` instance — registration, capsule capture, source ingestion through AI extraction and expert review, golden evaluation and immutable publication, cited query with unsupported-question abstention and license revocation, organization creation, MFA enrollment/enforcement, org-owned capsule visibility, and expert credential submission.
