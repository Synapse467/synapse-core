import {
  BadRequestException,
  ForbiddenException,
  ServiceUnavailableException,
  UnauthorizedException,
} from "@nestjs/common";
import {
  createHash,
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
});
export type Principal = {
  id: string;
  email: string;
  name: string;
  bio: string;
  domain: string;
  verificationStatus: string;
  stellarPublicKey?: string | null;
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

