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

Copy `.env.example` to `.env` and set the matching private AI service token. Local development credentials are not production credentials. The API listens on localhost:4000. Set `WEB_ORIGIN` to the exact web origin. Session cookies are HttpOnly, SameSite=Lax, Secure in production, stored server-side by token hash. Passwords use salted scrypt. Writes require the configured Origin.

API docs: `/v1/docs`. Client projections are in the web repository. The initial SQL migration includes foreign keys, usage-cap checks, and immutable version/audit triggers in addition to Prisma-managed tables; preserve these in future migrations.

Implemented: identity/session endpoints, workspace projection/actions, ownership-scoped capsule editing, private source upload/finalization with SHA-256 validation, idempotent source jobs, approval/rejection/correction, expert-authored evaluation cases, revision-specific publish gate, immutable snapshots/manifests, grants/revocation, public discovery, licensed conversations, provenance validation, serializable usage enforcement, ordered recoverable interview chunks and follow-up questions.

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

Do not deploy as production-complete: organization/membership and MFA flows, verification evidence review, scanner integration, provider retention policy, transcript redaction/deletion, full PRD endpoint aliases and generated request schemas, semantic/vector retrieval, complete audit coverage, general request idempotency, settlement orchestration, Stellar worker execution, and full integration/security tests remain. Interview completion records captured chunks but still needs assembly/transcription job orchestration. Stellar publication jobs are queued but have no submitting worker yet. No Testnet anchor is claimed.

MinIO uses its documented Quay registry because the Docker Hub image was unavailable. Reference: https://min.io/docs/minio/container/operations/install-deploy-manage/deploy-minio-single-node-multi-drive.html
