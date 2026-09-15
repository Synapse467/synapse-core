import { randomBytes } from "node:crypto";
import Redis from "ioredis";

const TTL_MS = 2 * 60 * 1000;
const memory = new Map<string, { nonce: string; expiresAt: number }>();
let redis: Redis | null | undefined;

function redisClient(): Redis | null {
  if (redis !== undefined) return redis;
  const url = process.env.REDIS_URL;
  if (!url) {
    redis = null;
    return null;
  }
  redis = new Redis(url, { maxRetriesPerRequest: 1, lazyConnect: true });
  redis.on("error", () => undefined);
  return redis;
}

/** PRD §21: wallet sign-in nonces live in Redis when available, never forever in process RAM. */
export async function issueWalletNonce(publicKey: string): Promise<string> {
  const nonce = randomBytes(32).toString("hex");
  const client = redisClient();
  if (client) {
    try {
      await client.set(`synapse:wallet-nonce:${publicKey}`, nonce, "PX", TTL_MS);
      return nonce;
    } catch {
      // Fall through to in-process store if Redis is briefly unreachable.
    }
  }
  memory.set(publicKey, { nonce, expiresAt: Date.now() + TTL_MS });
  return nonce;
}

export async function consumeWalletNonce(
  publicKey: string,
  nonce: string,
): Promise<boolean> {
  const client = redisClient();
  if (client) {
    try {
      const key = `synapse:wallet-nonce:${publicKey}`;
      const stored = await client.get(key);
      if (stored === nonce) {
        await client.del(key);
        return true;
      }
      if (stored) return false;
    } catch {
      // Fall through.
    }
  }
  const entry = memory.get(publicKey);
  if (!entry || entry.nonce !== nonce || Date.now() > entry.expiresAt)
    return false;
  memory.delete(publicKey);
  return true;
}
