import {
  Body,
  Controller,
  Get,
  Post,
  Param,
  Req,
  NotFoundException,
  ForbiddenException,
  BadRequestException,
} from "@nestjs/common";
import { ApiTags, ApiOperation } from "@nestjs/swagger";
import type { FastifyRequest } from "fastify";
import { randomUUID } from "node:crypto";
import { z } from "zod";
import { Database } from "./database";
import { ProductController } from "./controller";
import {
  parse,
  randomBase32Secret,
  totpUri,
  verifyTotp,
  isPlatformReviewer,
  type Principal,
} from "./core";

/**
 * Organizations, MFA, expert credentials, and platform-moderator roles.
 * Credential review is gated on the caller's persisted `User.platformRole`
 * (ADMIN or REVIEWER). `PLATFORM_ADMIN_EMAILS` / `PLATFORM_REVIEWER_EMAILS`
 * only bootstrap those roles on first authenticated request — they are not
 * a substitute for the role column after that.
 */
async function requireMembership(
  db: Database,
  organizationId: string,
  userId: string,
) {
  const membership = await db.organizationMembership.findUnique({
    where: { organizationId_userId: { organizationId, userId } },
  });
  if (!membership) throw new NotFoundException("Organization not found.");
  return membership;
}

async function requireAdmin(
  db: Database,
  organizationId: string,
  userId: string,
) {
  const membership = await requireMembership(db, organizationId, userId);
  if (membership.role !== "ADMIN")
    throw new ForbiddenException("Only organization admins may do this.");
  return membership;
}

/**
 * Enforces PRD §21 "MFA for organization admins": any admin-only,
 * organization-mutating action requires a currently valid TOTP code, not
 * merely a password-authenticated session. An admin who has not yet
 * enrolled MFA cannot perform these actions until they do — MFA for admins
 * is mandatory, not optional, matching the PRD wording.
 */
function requireCurrentMfaCode(
  membership: { totpSecret: string | null; totpEnabled: boolean },
  code: unknown,
) {
  if (!membership.totpEnabled || !membership.totpSecret)
    throw new ForbiddenException(
      "Enroll and verify multi-factor authentication before performing organization-admin actions.",
    );
  if (typeof code !== "string" || !verifyTotp(membership.totpSecret, code))
    throw new ForbiddenException(
      "A valid multi-factor authentication code is required for this action.",
    );
}

@ApiTags("Organizations")
@Controller("v1")
export class OrganizationsController {
  constructor(
    private db: Database,
    private auth: ProductController,
  ) {}

  private async me(req: FastifyRequest): Promise<Principal> {
    return this.auth.user(req);
  }

  @Post("organizations")
  @ApiOperation({ summary: "Create an organization (creator becomes admin)" })
  async create(@Req() req: FastifyRequest, @Body() body: unknown) {
    const user = await this.me(req);
    const { name } = parse(
      z.object({ name: z.string().trim().min(2).max(120) }),
      body,
    );
    const id = randomUUID();
    const slug = `${name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .slice(0, 60)}-${id.slice(0, 8)}`;
    return this.db.$transaction(async (tx) => {
      const organization = await tx.organization.create({
        data: { id, name, slug },
      });
      await tx.organizationMembership.create({
        data: { organizationId: id, userId: user.id, role: "ADMIN" },
      });
      return organization;
    });
  }

  @Get("organizations")
  @ApiOperation({ summary: "List organizations the caller belongs to" })
  async list(@Req() req: FastifyRequest) {
    const user = await this.me(req);
    const memberships = await this.db.organizationMembership.findMany({
      where: { userId: user.id },
    });
    const organizations = await this.db.organization.findMany({
      where: { id: { in: memberships.map((m) => m.organizationId) } },
    });
    return organizations.map((org) => {
      const membership = memberships.find((m) => m.organizationId === org.id);
      return {
        ...org,
        role: membership?.role,
        mfaEnabled: membership?.totpEnabled ?? false,
      };
    });
  }

  @Post("organizations/:id/members")
  @ApiOperation({ summary: "Invite a member (admin-only, requires MFA)" })
  async invite(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.me(req);
    const admin = await requireAdmin(this.db, id, user.id);
    const value = parse(
      z.object({
        email: z.email().transform((s) => s.toLowerCase()),
        role: z.enum(["ADMIN", "MEMBER"]).default("MEMBER"),
        mfaCode: z.string(),
      }),
      body,
    );
    requireCurrentMfaCode(admin, value.mfaCode);
    const invitee = await this.db.user.findUnique({
      where: { email: value.email },
    });
    if (!invitee)
      throw new NotFoundException(
        "No Synapse account exists for that email yet. Ask them to register first.",
      );
    return this.db.organizationMembership.upsert({
      where: {
        organizationId_userId: { organizationId: id, userId: invitee.id },
      },
      create: { organizationId: id, userId: invitee.id, role: value.role },
      update: { role: value.role },
    });
  }

  @Get("organizations/:id/members")
  @ApiOperation({ summary: "List members of an organization (member-only)" })
  async members(@Param("id") id: string, @Req() req: FastifyRequest) {
    const user = await this.me(req);
    await requireMembership(this.db, id, user.id);
    const memberships = await this.db.organizationMembership.findMany({
      where: { organizationId: id },
    });
    const users = await this.db.user.findMany({
      where: { id: { in: memberships.map((m) => m.userId) } },
      select: { id: true, name: true, email: true },
    });
    return memberships.map((m) => ({
      ...users.find((u) => u.id === m.userId),
      role: m.role,
      mfaEnabled: m.totpEnabled,
    }));
  }

  @Post("organizations/:id/mfa/enroll")
  @ApiOperation({
    summary: "Begin admin MFA enrollment: returns a TOTP secret + QR URI",
  })
  async enrollMfa(@Param("id") id: string, @Req() req: FastifyRequest) {
    const user = await this.me(req);
    const admin = await requireAdmin(this.db, id, user.id);
    const secret = randomBase32Secret();
    await this.db.organizationMembership.update({
      where: { id: admin.id },
      data: { totpSecret: secret, totpEnabled: false },
    });
    return {
      secret,
      otpauthUri: totpUri(secret, user.email, "Synapse"),
    };
  }

  @Post("organizations/:id/mfa/verify")
  @ApiOperation({
    summary: "Confirm a TOTP code to activate admin MFA enforcement",
  })
  async verifyMfa(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.me(req);
    const admin = await requireAdmin(this.db, id, user.id);
    const { code } = parse(z.object({ code: z.string() }), body);
    if (!admin.totpSecret)
      throw new BadRequestException("Start enrollment first.");
    if (!verifyTotp(admin.totpSecret, code))
      throw new BadRequestException("Incorrect or expired code.");
    await this.db.organizationMembership.update({
      where: { id: admin.id },
      data: { totpEnabled: true },
    });
    return { mfaEnabled: true };
  }

  // ─── Expert credentials (PRD §7 ExpertCredential, §12 POST /experts/me/verification) ───

  @Post("experts/me/verification")
  @ApiOperation({ summary: "Submit expert-credential verification evidence" })
  async submitCredential(@Req() req: FastifyRequest, @Body() body: unknown) {
    const user = await this.me(req);
    const value = parse(
      z.object({
        type: z.string().trim().min(2).max(80),
        issuer: z.string().trim().min(2).max(120),
        evidenceObjectKey: z.string().max(300).optional(),
      }),
      body,
    );
    return this.db.expertCredential.create({
      data: { ...value, expertId: user.id },
    });
  }

  @Get("experts/me/credentials")
  @ApiOperation({ summary: "List the caller's submitted credentials" })
  async myCredentials(@Req() req: FastifyRequest) {
    const user = await this.me(req);
    return this.db.expertCredential.findMany({
      where: { expertId: user.id },
      orderBy: { createdAt: "desc" },
    });
  }

  @Get("experts/credentials")
  @ApiOperation({
    summary: "List pending credential submissions (platform reviewers only)",
  })
  async pendingCredentials(@Req() req: FastifyRequest) {
    const user = await this.me(req);
    if (!isPlatformReviewer(user))
      throw new ForbiddenException(
        "This account is not authorized to review credential evidence.",
      );
    return this.db.expertCredential.findMany({
      where: { verificationStatus: "PENDING" },
      orderBy: { createdAt: "desc" },
    });
  }

  @Post("admin/platform-roles")
  @ApiOperation({
    summary: "Assign a platform role (ADMIN only)",
  })
  async assignPlatformRole(@Req() req: FastifyRequest, @Body() body: unknown) {
    const actor = await this.me(req);
    if (actor.platformRole !== "ADMIN")
      throw new ForbiddenException("Only a platform admin may assign roles.");
    const input = parse(
      z.object({
        email: z.email().transform((s) => s.toLowerCase()),
        role: z.enum(["NONE", "REVIEWER", "ADMIN"]),
      }),
      body,
    );
    const target = await this.db.user.findUnique({ where: { email: input.email } });
    if (!target) throw new NotFoundException("User not found.");
    return this.db.user.update({
      where: { id: target.id },
      data: { platformRole: input.role },
    });
  }

  @Post("experts/credentials/:id/review")
  @ApiOperation({
    summary: "Review submitted evidence (platform ADMIN or REVIEWER)",
  })
  async reviewCredential(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.me(req);
    if (!isPlatformReviewer(user))
      throw new ForbiddenException(
        "This account is not authorized to review credential evidence.",
      );
    const { status } = parse(
      z.object({ status: z.enum(["APPROVED", "REJECTED"]) }),
      body,
    );
    const credential = await this.db.expertCredential.findUnique({
      where: { id },
    });
    if (!credential) throw new NotFoundException("Credential not found.");
    const updated = await this.db.expertCredential.update({
      where: { id },
      data: {
        verificationStatus: status,
        reviewedBy: user.id,
        reviewedAt: new Date(),
      },
    });
    if (status === "APPROVED") {
      const approvedCount = await this.db.expertCredential.count({
        where: { expertId: credential.expertId, verificationStatus: "APPROVED" },
      });
      if (approvedCount > 0)
        await this.db.user.update({
          where: { id: credential.expertId },
          data: { verificationStatus: "VERIFIED" },
        });
    }
    return updated;
  }
}
