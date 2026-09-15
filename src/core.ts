import {
  BadRequestException,
  ForbiddenException,
  ServiceUnavailableException,
  UnauthorizedException,
} from "@nestjs/common";
import {
  createHash,
  createHmac,
  randomBytes,
  scrypt as scryptCallback,
  timingSafeEqual,
} from "node:crypto";
import { promisify } from "node:util";
import { z } from "zod";
const scrypt = promisify(scryptCallback);
export const hash = (text: string | Buffer) =>
  createHash("sha256").update(text).digest("hex");
export function canonical(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  return `{${Object.keys(value as object)
    .sort()
    .map(
      (k) =>
        `${JSON.stringify(k)}:${canonical((value as Record<string, unknown>)[k])}`,
    )
    .join(",")}}`;
}
export function parse<T>(schema: z.ZodType<T>, value: unknown): T {
  const result = schema.safeParse(value);
  if (!result.success)
    throw new BadRequestException(
      result.error.issues
        .map((i) => `${i.path.join(".")}: ${i.message}`)
        .join("; "),
    );
  return result.data;
}
export async function passwordHash(password: string) {
  const salt = randomBytes(24).toString("hex");
  const derived = (await scrypt(password, salt, 64)) as Buffer;
  return `${salt}:${derived.toString("hex")}`;
}
export async function verifyPassword(password: string, stored: string) {
  const [salt, key] = stored.split(":");
  const expected = Buffer.from(key || "", "hex");
  const actual = (await scrypt(
    password,
    salt || "unknown-account-salt",
    64,
  )) as Buffer;
  return expected.length === actual.length && timingSafeEqual(expected, actual);
}
export const authInput = z.object({
  email: z.email().transform((s) => s.toLowerCase()),
  password: z.string().min(12).max(256),
  name: z.string().trim().min(2).max(120).optional(),
});
export const capsuleInput = z.object({
  title: z.string().trim().min(3).max(120),
  domain: z.string().trim().min(2).max(80),
  scope: z.string().trim().min(15).max(2000),
  visibility: z.enum(["PRIVATE", "PUBLIC"]).default("PRIVATE"),
});
export const grantInput = z.object({
  name: z.string().trim().min(2).max(100).default("Team learning"),
  grantee: z.email().transform((s) => s.toLowerCase()),
  audience: z.string().min(2).max(100).default("Named user"),
  purposes: z
    .union([z.string(), z.array(z.string())])
    .transform((v) =>
      typeof v === "string"
        ? v
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean)
        : v,
    )
    .refine((v) => v.length > 0 && v.length <= 10),
  aiTrainingAllowed: z.boolean().default(false),
  commercialUse: z.boolean().default(false),
  derivativeUse: z.boolean().default(false),
  usageLimit: z.coerce.number().int().min(1).max(1000000),
  days: z.coerce.number().int().min(1).max(3650),
  pricePerUnitMinor: z.coerce.number().int().min(0).max(1_000_000_000).default(0),
  assetCode: z.string().trim().min(1).max(12).default("USD"),
});
export type Principal = {
  id: string;
  email: string;
  name: string;
  bio: string;
  domain: string;
  verificationStatus: string;
  stellarPublicKey?: string | null;
  platformRole: string;
};
export type ApprovedItem = {
  id: string;
  sourceId: string;
  contributorId: string;
  contributor: string;
  kind: string;
  text: string;
  proposal: string;
  status: string;
  confidence: number;
  citation: unknown;
};
export function ensureLicense(
  grant: {
    status: string;
    startsAt: Date;
    expiresAt: Date;
    used: number;
    usageLimit: number;
    purposes: string[];
    grantee: string;
  } | null,
  email: string,
  purpose: string,
) {
  if (
    !grant ||
    grant.grantee !== email ||
    grant.status !== "ACTIVE" ||
    grant.startsAt > new Date() ||
    grant.expiresAt <= new Date() ||
    grant.used >= grant.usageLimit ||
    !grant.purposes.includes(purpose)
  )
    throw new ForbiddenException(
      "An active license for this user and purpose is required. Access may have expired, been revoked, or reached its usage limit.",
    );
  return grant;
}
export async function ai<T>(path: string, body: unknown): Promise<T> {
  if (!process.env.AI_SERVICE_TOKEN || process.env.AI_SERVICE_TOKEN.length < 32)
    throw new ServiceUnavailableException(
      "The private AI service is not configured.",
    );
  try {
    const response = await fetch(
      `${process.env.AI_BASE_URL || "http://127.0.0.1:8000"}/internal/v1/${path}`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${process.env.AI_SERVICE_TOKEN}`,
        },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(90000),
      },
    );
    if (!response.ok) throw new Error("AI processing failed");
    return (await response.json()) as T;
  } catch {
    throw new ServiceUnavailableException(
      "AI processing is currently unavailable. Your saved source has not been published.",
    );
  }
}
export function principalRequired(user: Principal | undefined): Principal {
  if (!user)
    throw new UnauthorizedException("Sign in to access your workspace.");
  return user;
}

// ─── TOTP (RFC 6238) for organization-admin MFA (PRD §21) ──────────────────
// Self-contained (no external dependency) HMAC-SHA1, 6-digit, 30s-step TOTP,
// matching the algorithm every standard authenticator app (Google
// Authenticator, Authy, 1Password, etc.) implements.
const BASE32_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
export function randomBase32Secret(byteLength = 20): string {
  const buf = randomBytes(byteLength);
  let bits = 0;
  let value = 0;
  let output = "";
  for (const byte of buf) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      output += BASE32_ALPHABET[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) output += BASE32_ALPHABET[(value << (5 - bits)) & 31];
  return output;
}
function base32Decode(input: string): Buffer {
  const clean = input.toUpperCase().replace(/[^A-Z2-7]/g, "");
  let bits = 0;
  let value = 0;
  const bytes: number[] = [];
  for (const char of clean) {
    const idx = BASE32_ALPHABET.indexOf(char);
    if (idx === -1) continue;
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      bytes.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(bytes);
}
function totpAt(secret: string, counter: number, digits = 6): string {
  const key = base32Decode(secret);
  const buf = Buffer.alloc(8);
  buf.writeUInt32BE(Math.floor(counter / 2 ** 32), 0);
  buf.writeUInt32BE(counter % 2 ** 32, 4);
  const digest = createHmac("sha1", key).update(buf).digest();
  const offset = digest[digest.length - 1] & 0xf;
  const binCode =
    ((digest[offset] & 0x7f) << 24) |
    ((digest[offset + 1] & 0xff) << 16) |
    ((digest[offset + 2] & 0xff) << 8) |
    (digest[offset + 3] & 0xff);
  return (binCode % 10 ** digits).toString().padStart(digits, "0");
}
export function totpUri(
  secret: string,
  label: string,
  issuer = "Synapse",
): string {
  return `otpauth://totp/${encodeURIComponent(issuer)}:${encodeURIComponent(label)}?secret=${secret}&issuer=${encodeURIComponent(issuer)}&algorithm=SHA1&digits=6&period=30`;
}
export function verifyTotp(
  secret: string,
  token: string,
  windowSteps = 1,
  stepSeconds = 30,
): boolean {
  if (!/^\d{6}$/.test(token)) return false;
  const counter = Math.floor(Date.now() / 1000 / stepSeconds);
  for (let error = -windowSteps; error <= windowSteps; error++) {
    const candidate = totpAt(secret, counter + error);
    const a = Buffer.from(candidate);
    const b = Buffer.from(token);
    if (a.length === b.length && timingSafeEqual(a, b)) return true;
  }
  return false;
}

export function scanContent(text: string): { safe: boolean; reason?: string } {
  const lower = text.toLowerCase();
  const dangerousPatterns = [
    /<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/i,
    /javascript:/i,
    /data:text\/html/i,
    /ignore\s+(?:all\s+)?(?:previous|prior)\s+instructions/i,
    /system\s+prompt\s+override/i,
    /disregard\s+(?:all\s+)?guidance/i,
  ];
  for (const pattern of dangerousPatterns) {
    if (pattern.test(lower)) {
      return {
        safe: false,
        reason: "Content flagged by security scan (untrusted script or prompt-injection pattern).",
      };
    }
  }
  return { safe: true };
}

/** Industry-standard inert antivirus test file (https://www.eicar.org/). */
export const EICAR_SIGNATURE =
  "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*";
const EXECUTABLE_MAGIC: Buffer[] = [
  Buffer.from("MZ"),
  Buffer.from([0x7f, 0x45, 0x4c, 0x46]),
  Buffer.from([0xfe, 0xed, 0xfa, 0xce]),
  Buffer.from([0xfe, 0xed, 0xfa, 0xcf]),
  Buffer.from([0xca, 0xfe, 0xba, 0xbe]),
  Buffer.from([0xcf, 0xfa, 0xed, 0xfe]),
];

export function scanBinary(
  content: Buffer,
  filename: string,
): { safe: boolean; reason?: string } {
  if (content.includes(Buffer.from(EICAR_SIGNATURE)))
    return {
      safe: false,
      reason: "Rejected: file matches the EICAR antivirus test signature.",
    };
  if (EXECUTABLE_MAGIC.some((magic) => content.subarray(0, magic.length).equals(magic)))
    return {
      safe: false,
      reason: `Rejected: '${filename}' is a compiled executable, not a source document.`,
    };
  return { safe: true };
}

/**
 * Heuristic scan plus optional real ClamAV. If `CLAMAV_REQUIRED=true` and
 * `CLAMAV_CMD` is unset/unusable, this throws — it never reports "clean"
 * when the configured production scanner cannot actually run.
 */
export async function scanUploadedFile(
  content: Buffer,
  filename: string,
): Promise<{ safe: boolean; reason?: string }> {
  const heuristic = scanBinary(content, filename);
  if (!heuristic.safe) return heuristic;
  const cmd = (process.env.CLAMAV_CMD || "").trim();
  const required = process.env.CLAMAV_REQUIRED === "true";
  if (!cmd) {
    if (required)
      throw new ServiceUnavailableException(
        "File scanning is required (CLAMAV_REQUIRED=true) but CLAMAV_CMD is not configured.",
      );
    return { safe: true };
  }
  const { mkdtemp, writeFile, unlink, rmdir } = await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const { execFile } = await import("node:child_process");
  const dir = await mkdtemp(join(tmpdir(), "synapse-scan-"));
  const file = join(dir, filename.replace(/[^A-Za-z0-9._-]+/g, "_") || "upload.bin");
  await writeFile(file, content);
  try {
    const [bin, ...args] = cmd.split(/\s+/);
    await new Promise<void>((resolve, reject) => {
      execFile(bin, [...args, file], { timeout: 30000 }, (err, stdout, stderr) => {
        const output = `${stdout || ""}${stderr || ""}`;
        if (!err) return resolve();
        if (typeof err.code === "number" && err.code === 1)
          return reject(
            Object.assign(new Error("Rejected: ClamAV reported malware."), {
              clamav: true,
              output,
            }),
          );
        reject(err);
      });
    });
    return { safe: true };
  } catch (err) {
    if (err && typeof err === "object" && "clamav" in err)
      return { safe: false, reason: "Rejected: ClamAV reported malware." };
    if (required)
      throw new ServiceUnavailableException(
        "Configured ClamAV scanner could not run. Refusing to accept the upload as clean.",
      );
    throw new ServiceUnavailableException(
      "File scanner failed. Retry after the scanning service is healthy.",
    );
  } finally {
    await unlink(file).catch(() => undefined);
    await rmdir(dir).catch(() => undefined);
  }
}

export async function bootstrapPlatformRole(user: {
  id: string;
  email: string;
  platformRole: string;
}): Promise<string> {
  if (user.platformRole !== "NONE") return user.platformRole;
  const email = user.email.toLowerCase();
  if (platformAdminEmails().includes(email)) return "ADMIN";
  const reviewers = (process.env.PLATFORM_REVIEWER_EMAILS || "")
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  if (reviewers.includes(email)) return "REVIEWER";
  return "NONE";
}

/** PRD §21 transcript redaction: replace emails, phones, SSNs, and card-like numbers. */
export function redactPii(text: string): { text: string; replacements: number } {
  const patterns: RegExp[] = [
    /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi,
    /\b(?:\+?\d{1,3}[-.\s]?)?(?:\(?\d{3}\)?[-.\s]?){2}\d{4}\b/g,
    /\b\d{3}-\d{2}-\d{4}\b/g,
    /\b(?:\d[ -]*?){13,19}\b/g,
  ];
  let replacements = 0;
  let next = text;
  for (const pattern of patterns) {
    next = next.replace(pattern, () => {
      replacements += 1;
      return "[REDACTED]";
    });
  }
  return { text: next, replacements };
}

export function platformAdminEmails(): string[] {
  return (process.env.PLATFORM_ADMIN_EMAILS || "")
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
}

export function isPlatformReviewer(user: {
  platformRole: string;
  email: string;
}): boolean {
  if (user.platformRole === "ADMIN" || user.platformRole === "REVIEWER")
    return true;
  const extra = (process.env.PLATFORM_REVIEWER_EMAILS || "")
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  return extra.includes(user.email.toLowerCase());
}

