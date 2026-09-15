import {
  Body,
  Controller,
  Get,
  Post,
  Param,
  Req,
  Res,
  UnauthorizedException,
  NotFoundException,
  ForbiddenException,
  BadRequestException,
} from "@nestjs/common";
import { ApiTags, ApiOperation, ApiBody } from "@nestjs/swagger";
import type { FastifyRequest, FastifyReply } from "fastify";
import { randomBytes } from "node:crypto";
import { Keypair } from "@stellar/stellar-sdk";
import { Prisma } from "@prisma/client";
import { z } from "zod";
import { Database } from "./database";
import { WorkspaceService } from "./workspace";
import {
  parse,
  authInput,
  passwordHash,
  verifyPassword,
  hash,
  ensureLicense,
  ai,
  type Principal,
  type ApprovedItem,
} from "./core";

// In-memory nonce store (per-process, short TTL)
// In production this should be Redis with a 2-minute TTL
const walletNonces = new Map<string, { nonce: string; expiresAt: number }>();
const NONCE_TTL_MS = 2 * 60 * 1000; // 2 minutes
function issueNonce(publicKey: string): string {
  const nonce = randomBytes(32).toString("hex");
  walletNonces.set(publicKey, { nonce, expiresAt: Date.now() + NONCE_TTL_MS });
  return nonce;
}
function consumeNonce(publicKey: string, nonce: string): boolean {
  const entry = walletNonces.get(publicKey);
  if (!entry || entry.nonce !== nonce || Date.now() > entry.expiresAt) return false;
  walletNonces.delete(publicKey);
  return true;
}
@ApiTags("Synapse v1")
@Controller("v1")
export class ProductController {
  constructor(
    private db: Database,
    private workspace: WorkspaceService,
  ) {}
  async user(req: FastifyRequest): Promise<Principal> {
    const token = req.cookies.synapse_session;
    if (!token) throw new UnauthorizedException("Sign in to your workspace.");
    const session = await this.db.session.findUnique({
      where: { id: hash(token) },
    });
    if (!session || session.expiresAt <= new Date())
      throw new UnauthorizedException("Your session has expired.");
    const user = await this.db.user.findUnique({
      where: { id: session.userId },
    });
    if (!user) throw new UnauthorizedException();
    return user;
  }
  private async session(userId: string, res: FastifyReply) {
    const token = randomBytes(32).toString("base64url");
    await this.db.session.create({
      data: {
        id: hash(token),
        userId,
        expiresAt: new Date(Date.now() + 7 * 86400000),
      },
    });
    res.setCookie("synapse_session", token, {
      httpOnly: true,
      secure: process.env.NODE_ENV === "production",
      sameSite: "lax",
      path: "/",
      maxAge: 7 * 86400,
    });
  }
  @Post("auth/register")
  @ApiOperation({ summary: "Create an account and secure session" })
  @ApiBody({
    schema: {
      type: "object",
      required: ["name", "email", "password"],
      properties: {
        name: { type: "string" },
        email: { type: "string", format: "email" },
        password: { type: "string", minLength: 12 },
      },
    },
  })
  async register(
    @Body() body: unknown,
    @Res({ passthrough: true }) res: FastifyReply,
  ) {
    const input = parse(
      authInput.extend({ name: z.string().trim().min(2).max(120) }),
      body,
    );
    if (await this.db.user.findUnique({ where: { email: input.email } }))
      throw new BadRequestException(
        "Unable to register this email. Try signing in.",
      );
    const user = await this.db.user.create({
      data: {
        email: input.email,
        name: input.name,
        passwordHash: await passwordHash(input.password),
      },
    });
    await this.session(user.id, res);
    return { id: user.id, name: user.name, email: user.email };
  }
  @Post("auth/login")
  @ApiOperation({ summary: "Sign in with email and password" })
  async login(
    @Body() body: unknown,
    @Res({ passthrough: true }) res: FastifyReply,
  ) {
    const input = parse(authInput, body);
    const user = await this.db.user.findUnique({
      where: { email: input.email },
    });
    const valid = await verifyPassword(
      input.password,
      user?.passwordHash || "unknown:00",
    );
    if (!user || !valid)
      throw new UnauthorizedException("Email or password is incorrect.");
    await this.session(user.id, res);
    return { id: user.id, name: user.name, email: user.email };
  }
  @Post("auth/logout") async logout(
    @Req() req: FastifyRequest,
    @Res({ passthrough: true }) res: FastifyReply,
  ) {
    if (req.cookies.synapse_session)
      await this.db.session.deleteMany({
        where: { id: hash(req.cookies.synapse_session) },
      });
    res.clearCookie("synapse_session", { path: "/" });
    return { ok: true };
  }

  // ─── Freighter wallet auth ──────────────────────────────────────────

  @Post("auth/wallet-challenge")
  @ApiOperation({ summary: "Request a sign-in challenge nonce for a Stellar public key" })
  async walletChallenge(@Body() body: unknown) {
    const { publicKey } = parse(
      z.object({ publicKey: z.string().regex(/^G[A-Z2-7]{55}$/, "Invalid Stellar public key") }),
      body,
    );
    const nonce = issueNonce(publicKey);
    return { message: `Synapse sign-in: ${nonce}`, nonce };
  }

  @Post("auth/wallet-verify")
  @ApiOperation({ summary: "Verify Freighter signature and open a session" })
  async walletVerify(
    @Body() body: unknown,
    @Res({ passthrough: true }) res: FastifyReply,
  ) {
    const input = parse(
      z.object({
        publicKey: z.string().regex(/^G[A-Z2-7]{55}$/),
        nonce: z.string().min(1),
        signedMessage: z.string().min(1),
        displayName: z.string().trim().min(2).max(120).optional(),
      }),
      body,
    );
    if (!consumeNonce(input.publicKey, input.nonce))
      throw new UnauthorizedException("Challenge expired or invalid. Request a new one.");
    let valid = false;
    try {
      const expectedMessage = `Synapse sign-in: ${input.nonce}`;
      const msgBytes = Buffer.from(expectedMessage, "utf-8");
      const sigBytes = Buffer.from(input.signedMessage, "base64");
      const keypair = Keypair.fromPublicKey(input.publicKey);
      valid = keypair.verify(msgBytes, sigBytes);
    } catch {
      throw new BadRequestException("Signature could not be decoded.");
    }
    if (!valid)
      throw new UnauthorizedException("Wallet signature does not match the challenge.");
    let user = await this.db.user.findFirst({ where: { stellarPublicKey: input.publicKey } });
    if (!user) {
      const syntheticEmail = `${input.publicKey.toLowerCase()}@wallet.synapse`;
      user = await this.db.user.create({
        data: {
          email: syntheticEmail,
          name: input.displayName ?? `Expert ${input.publicKey.slice(0, 8)}`,
          passwordHash: "wallet-auth:no-password",
          stellarPublicKey: input.publicKey,
        },
      });
    }
    await this.session(user.id, res);
    return { id: user.id, name: user.name, email: user.email, stellarPublicKey: user.stellarPublicKey, authMethod: "wallet" };
  }

  @Get("me") async me(@Req() req: FastifyRequest) {
    const u = await this.user(req);
    return {
      id: u.id,
      name: u.name,
      email: u.email,
      bio: u.bio,
      domain: u.domain,
      verificationStatus: u.verificationStatus,
      stellarPublicKey: u.stellarPublicKey ?? null,
    };
  }
  @Get("workspace") async getWorkspace(@Req() req: FastifyRequest) {
    return this.workspace.snapshot(await this.user(req));
  }
  @Post("workspace/actions")
  @ApiOperation({ summary: "Execute an authorized expert workspace operation" })
  async action(@Req() req: FastifyRequest, @Body() body: unknown) {
    return this.workspace.action(await this.user(req), body);
  }
  @Get("capsules") async publicCapsules() {
    return this.db.capsule.findMany({
      where: { visibility: "PUBLIC", status: "PUBLISHED" },
      select: {
        id: true,
        title: true,
        domain: true,
        scope: true,
        visibility: true,
        status: true,
        currentVersion: true,
        updatedAt: true,
      },
      take: 100,
      orderBy: { updatedAt: "desc" },
    });
  }
  @Get("capsules/:id/public") async publicCapsule(@Param("id") id: string) {
    const capsule = await this.db.capsule.findFirst({
      where: { id, visibility: "PUBLIC", status: "PUBLISHED" },
    });
    if (!capsule) throw new NotFoundException("Public capsule not found.");
    const contributor = await this.db.user.findUnique({
      where: { id: capsule.ownerId },
      select: { name: true },
    });
    const templates = await this.db.licenseTemplate.findMany({
      where: { capsuleId: id },
    });
    return {
      capsule: {
        id: capsule.id,
        title: capsule.title,
        domain: capsule.domain,
        scope: capsule.scope,
        visibility: capsule.visibility,
        status: capsule.status,
        version: capsule.currentVersion,
      },
      contributor: contributor?.name,
      templates,
    };
  }
  @Post("capsules/:id/access-requests") async requestAccess(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.user(req);
    const { templateId } = parse(
      z.object({ templateId: z.string().uuid() }),
      body,
    );
    const template = await this.db.licenseTemplate.findFirst({
      where: { id: templateId, capsuleId: id },
    });
    const capsule = await this.db.capsule.findFirst({
      where: { id, visibility: "PUBLIC", status: "PUBLISHED" },
    });
    if (!template || !capsule) throw new NotFoundException();
    return this.db.accessRequest.upsert({
      where: { templateId_userId: { templateId, userId: user.id } },
      create: { capsuleId: id, templateId, userId: user.id },
      update: {},
    });
  }
  @Get("capsules/:id/query-context") async queryContext(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
  ) {
    const user = await this.user(req);
    const grants = await this.db.licenseGrant.findMany({
      where: {
        capsuleId: id,
        grantee: user.email,
        status: "ACTIVE",
        expiresAt: { gt: new Date() },
        startsAt: { lte: new Date() },
      },
    });
    if (!grants.length)
      throw new ForbiddenException("An active capsule license is required.");
    const capsule = await this.db.capsule.findUniqueOrThrow({ where: { id } });
    const versions = await this.db.capsuleVersion.findMany({
      where: { capsuleId: id },
      select: { version: true, publishedAt: true, manifestHash: true },
    });
    return {
      profile: {
        name: user.name,
        email: user.email,
        bio: user.bio,
        domain: user.domain,
      },
      capsules: [{ ...capsule, version: capsule.currentVersion, versions }],
      licenses: grants,
      knowledge: [],
      sources: [],
      evaluations: [],
      usage: [],
    };
  }
  @Post("capsules/:id/conversations") async conversation(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.user(req);
    const { version, purpose } = parse(
      z.object({
        version: z.string().max(30),
        purpose: z.string().min(1).max(100),
      }),
      body,
    );
    const grant = await this.db.licenseGrant.findFirst({
      where: {
        capsuleId: id,
        grantee: user.email,
        status: "ACTIVE",
        purposes: { has: purpose },
        expiresAt: { gt: new Date() },
      },
    });
    ensureLicense(grant, user.email, purpose);
    const published = await this.db.capsuleVersion.findUnique({
      where: { capsuleId_version: { capsuleId: id, version } },
    });
    if (!published) throw new NotFoundException("Published version not found.");
    return this.db.conversation.create({
      data: { capsuleId: id, userId: user.id, version, purpose },
    });
  }
  @Post("capsules/:id/conversations/:conversationId/messages") async message(
    @Param("id") id: string,
    @Param("conversationId") conversationId: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.user(req);
    const input = parse(
      z.object({
        query: z.string().trim().min(1).max(4000),
        purpose: z.string().min(1).max(100),
        version: z.string().max(30),
        idempotencyKey: z.string().uuid(),
      }),
      body,
    );
    const key = hash(`${user.id}:${input.idempotencyKey}`);
    const previous = await this.db.message.findUnique({
      where: { requestKey: key },
    });
    if (previous) {
      if (
        previous.conversationId !== conversationId ||
        previous.question !== input.query
      )
        throw new BadRequestException(
          "Idempotency key already used for another request.",
        );
      return previous.answer;
    }
    const conversation = await this.db.conversation.findFirst({
      where: {
        id: conversationId,
        capsuleId: id,
        userId: user.id,
        version: input.version,
        purpose: input.purpose,
      },
    });
    if (!conversation) throw new NotFoundException("Conversation not found.");
    const version = await this.db.capsuleVersion.findUniqueOrThrow({
      where: { capsuleId_version: { capsuleId: id, version: input.version } },
    });
    const grant = await this.db.licenseGrant.findFirst({
      where: {
        capsuleId: id,
        grantee: user.email,
        status: "ACTIVE",
        purposes: { has: input.purpose },
        expiresAt: { gt: new Date() },
      },
    });
    ensureLicense(grant, user.email, input.purpose);
    const knowledge = version.knowledge as unknown as ApprovedItem[];
    const answer = await ai<{
      text: string;
      abstained: boolean;
      citations: {
        id: string;
        source: string;
        quote: string;
        contributor: string;
        knowledgeId: string;
      }[];
      version: string;
    }>("query", {
      capsuleId: id,
      version: input.version,
      query: input.query,
      knowledge,
      idempotencyKey: key,
      allowedScope: { knowledgeIds: knowledge.map((k) => k.id) },
    });
    if (!answer.abstained && !answer.citations.length)
      throw new BadRequestException("Answer lacks source support.");
    for (const citation of answer.citations) {
      const item = knowledge.find(
        (k) => k.id === citation.knowledgeId && k.sourceId === citation.id,
      );
      if (
        !item ||
        item.contributor !== citation.contributor ||
        !item.text.includes(citation.quote)
      )
        throw new BadRequestException("Answer provenance check failed.");
    }
    return this.db.$transaction(
      async (tx) => {
        const latest = await tx.licenseGrant.findUnique({
          where: { id: grant!.id },
        });
        ensureLicense(latest, user.email, input.purpose);
        const duplicate = await tx.message.findUnique({
          where: { requestKey: key },
        });
        if (duplicate) return duplicate.answer;
        await tx.licenseGrant.update({
          where: { id: grant!.id },
          data: { used: { increment: 1 } },
        });
        await tx.usageEvent.create({
          data: {
            capsuleId: id,
            capsuleVersionId: version.id,
            licenseId: grant!.id,
            actorId: user.id,
            purpose: input.purpose,
            requestKey: key,
          },
        });
        const saved = await tx.message.create({
          data: {
            conversationId,
            userId: user.id,
            requestKey: key,
            question: input.query,
            answer: JSON.parse(JSON.stringify(answer)) as Prisma.InputJsonValue,
          },
        });
        return saved.answer;
      },
      { isolationLevel: Prisma.TransactionIsolationLevel.Serializable },
    );
  }
  @Get("health") async health() {
    await this.db.$queryRaw`SELECT 1`;
    return { status: "ok", service: "synapse-api", schemaVersion: "1.0" };
  }
}
