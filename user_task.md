# User tasks — what only a human can finish

This is the remaining work **you** have to do. The four repos now contain the product code. Nothing below is “write more application logic.” It is accounts, keys, machines, legal, and production hosting.

Do these in order. Skip a step only if you are staying on local Testnet forever.

---

## 1. Local secrets (do this first)

Copy each `.env.example` to `.env` and fill real values. Never commit `.env`.

### `synapse-api/.env`

| Variable | What you do |
|---|---|
| `AI_SERVICE_TOKEN` | Generate a random token **≥ 32 characters**. Use the **same** value in `synapse-ai/.env`. |
| `STELLAR_SIGNER_SECRET` | Stellar **secret key** (starts with `S…`) of an account that can pay Testnet/Mainnet fees. Get Testnet lumens from [Stellar Friendbot](https://friendbot.stellar.org). |
| `STELLAR_CAPSULE_CONTRACT_ID` | From `synapse-contracts/deployments/testnet.json` (already deployed on Testnet) **or** a Mainnet ID after you redeploy. |
| `STELLAR_LICENSE_CONTRACT_ID` | Same file / same deployment. |
| `STELLAR_USAGE_CONTRACT_ID` | Same file / same deployment. |
| `STELLAR_SETTLEMENT_CONTRACT_ID` | Same file (`settlement`). Must be set or settlement jobs fail honestly (503), they will not fake a hash. |
| `PLATFORM_ADMIN_EMAILS` | Your real operator email. First login with that email persists `User.platformRole=ADMIN`. Then you assign REVIEWER/ADMIN to others via `POST /v1/admin/platform-roles`. |
| `WEB_ORIGIN` | Exact browser origin, e.g. `http://localhost:3000` locally or `https://your-domain` in production. Writes are origin-locked. |
| `DATABASE_URL` / `REDIS_URL` / S3_* | Local docker-compose defaults are fine for development. Production: managed Postgres 18, Redis, and real S3/MinIO credentials. |

### `synapse-ai/.env`

| Variable | What you do |
|---|---|
| `AI_SERVICE_TOKEN` | **Must match** the API token. |
| `TRANSCRIPTION_API_URL` + `TRANSCRIPTION_API_KEY` | Real Whisper/Deepgram/AssemblyAI (or compatible) HTTP endpoint. Without these, production transcription returns **503** — it will not invent a transcript. |
| `TRANSCRIPTION_MODEL` | Provider model name if required. |
| `SYNAPSE_ENV` | Set to `production` in production. Only non-production allows the UTF-8 text-fixture adapter used by tests. |
| `CLAMAV_CMD` | Optional but recommended in production, e.g. `clamdscan --no-summary --stdout`. |
| `CLAMAV_REQUIRED` | Set `true` in production once ClamAV is installed. The API/AI will **refuse** uploads rather than call them clean. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Optional. Your Tempo/Honeycomb/Datadog OTLP HTTP traces URL. |

### `synapse-web/.env`

| Variable | What you do |
|---|---|
| `NEXT_PUBLIC_API_BASE_URL` | Public API URL including `/v1`, e.g. `https://api.your-domain.com/v1`. |

---

## 2. Host packages you must install (not npm)

On every machine that runs `synapse-ai` (including production containers):

```sh
# OCR for scanned PDFs (PRD §21). Without this, textless PDFs return 503.
sudo apt-get install -y tesseract-ocr

# Optional but recommended file scanner.
sudo apt-get install -y clamav clamav-daemon
sudo systemctl enable --now clamav-daemon
# Then set CLAMAV_CMD=clamdscan --no-summary --stdout and CLAMAV_REQUIRED=true
```

---

## 3. Stellar

**Already done on Testnet:** all five contracts are deployed. IDs and public tx hashes are in `synapse-contracts/deployments/testnet.json`. That file contains **no private keys**.

Your remaining Stellar work:

1. Fund `STELLAR_SIGNER_SECRET` (Testnet friendbot, or Mainnet XLM).
2. Copy the five contract IDs into `synapse-api/.env`.
3. **Mainnet** is a human decision: legal review, contract audit, then redeploy with Stellar CLI and replace the IDs. Do not treat Testnet IDs as Mainnet.
4. Per-expert self-custody (each expert’s Freighter wallet signing `register_capsule` / `grant`) is still a later product choice. Today the platform signer is the on-chain `require_auth` principal. That is documented, not hidden.
5. `Settlement.settle_split` records the **split proof** on-chain. It does **not** wire to a bank or send USDC by itself. If you want real money to move, you still need a payment rail (you pick the vendor).

---

## 4. Transcription provider

Pick one commercial speech-to-text HTTP API, create an account, put URL + key in `synapse-ai/.env`. The code already posts `multipart/form-data` with `Authorization: Bearer`.

Until you do this, interview audio and audio/video documents fail with a real 503.

---

## 5. Production hosting

PRD default:

| Piece | Typical place |
|---|---|
| `synapse-web` | Vercel (or any Node 24 host). Set `NEXT_PUBLIC_API_BASE_URL`. |
| `synapse-api` | Render / Fly / equivalent container. Bind Postgres, Redis, object storage. |
| `synapse-ai` | Private network only. **Do not** expose it to the public internet. |
| PostgreSQL 18 | Managed (RDS, Neon, Render Postgres, etc.). |
| Redis | Managed. |
| Object storage | Real S3 or equivalent. Not local MinIO. |
| TLS | HTTPS on web and API. Session cookies set `Secure` when `NODE_ENV=production`. |

Also set:

- DNS + exact `WEB_ORIGIN`
- backups for Postgres
- object-storage encryption / bucket policy (private)
- log drain (no source text — the API already redacts bodies)

---

## 6. First operator actions in the product

1. Register with the email in `PLATFORM_ADMIN_EMAILS`.
2. Confirm `GET /v1/me` shows `platformRole: "ADMIN"`.
3. `POST /v1/admin/platform-roles` to make human reviewers `REVIEWER`.
4. Create an organization in Studio → Organizations.
5. Enroll TOTP in an authenticator app (Google Authenticator, 1Password, etc.). Admin invites are blocked until you do.
6. Invite colleagues. They must already have accounts.
7. Review pending expert credentials (`GET /v1/experts/credentials`, then approve/reject).

---

## 7. Legal / policy (cannot be coded)

- Terms, privacy, and employment/IP agreements for organization-owned capsules.
- Whether you will actually pay contributors (payment vendor, tax, KYC).
- Identity-verification vendor (Persona/Onfido) if you want credentials checked by a third party instead of human reviewers. The product already stores evidence and a human review decision.
- Contract audit before Mainnet.

---

## 8. What you should **not** do

- Do not set `SYNAPSE_ENV` to anything other than `production` in production.
- Do not put Stellar secrets, DB URLs, or `AI_SERVICE_TOKEN` in the web app.
- Do not treat `/demo` as the product. It is a labelled browser-local preview only.
- Do not skip ClamAV in production if you accept arbitrary uploads; set `CLAMAV_REQUIRED=true` once the daemon is running.
- Do not expect `settle_split` to pay invoices. It is an on-chain split **record**.
