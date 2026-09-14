import {
  Injectable,
  NotFoundException,
  ForbiddenException,
  BadRequestException,
} from "@nestjs/common";
import { Prisma } from "@prisma/client";
import { randomUUID } from "node:crypto";
import { z } from "zod";
import { Database } from "./database";
import { Storage } from "./storage";
import { Jobs } from "./jobs";
import {
  ai,
  canonical,
  hash,
  parse,
  capsuleInput,
  grantInput,
  type Principal,
} from "./core";
const json = (value: unknown) =>
  JSON.parse(JSON.stringify(value)) as Prisma.InputJsonValue;
@Injectable()
export class WorkspaceService {
  constructor(
    private db: Database,
    private storage: Storage,
    private jobs: Jobs,
  ) {}
  async owned(user: Principal, id: string) {
    const capsule = await this.db.capsule.findUnique({ where: { id } });
    if (!capsule || capsule.ownerId !== user.id)
      throw new NotFoundException("Capsule not found.");
    return capsule;
  }
  async snapshot(user: Principal) {
    const capsules = await this.db.capsule.findMany({
      where: { ownerId: user.id },
      orderBy: { updatedAt: "desc" },
    });
    const ids = capsules.map((c) => c.id);
    const [versions, sources, knowledge, licenses, evaluations, usage] =
      await Promise.all([
        this.db.capsuleVersion.findMany({ where: { capsuleId: { in: ids } } }),
        this.db.sourceAsset.findMany({
          where: { capsuleId: { in: ids } },
          orderBy: { createdAt: "desc" },
        }),
        this.db.knowledgeItem.findMany({
          where: { capsuleId: { in: ids } },
          orderBy: { createdAt: "desc" },
        }),
        this.db.licenseGrant.findMany({ where: { ownerId: user.id } }),
        this.db.evaluation.findMany({
          where: { capsuleId: { in: ids } },
          orderBy: { createdAt: "desc" },
        }),
        this.db.usageEvent.findMany({
          where: { capsuleId: { in: ids } },
          orderBy: { occurredAt: "desc" },
          take: 500,
        }),
      ]);
    return {
      profile: {
        name: user.name,
        email: user.email,
        bio: user.bio,
        domain: user.domain,
      },
      capsules: capsules.map((c) => ({
        ...c,
        version: c.currentVersion,
        description: c.scope,
        versions: versions.filter((v) => v.capsuleId === c.id),
      })),
      sources: sources.map(s => ({id:s.id,capsuleId:s.capsuleId,contributor:s.contributor,title:s.title,type:s.type,text:s.text,status:s.status,createdAt:s.createdAt})),
      knowledge,
      licenses,
      evaluations: evaluations.filter(
        (e, i, all) => all.findIndex((v) => v.capsuleId === e.capsuleId) === i,
      ),
      usage,
    };
  }
  async action(user: Principal, input: unknown) {
    const action = parse(
      z.object({
        type: z.enum([
          "create-capsule",
          "profile",
          "add-source",
          "approve",
          "reject",
          "edit",
          "evaluate",
          "publish",
          "grant",
          "revoke",
        ]),
        capsuleId: z.string().optional(),
        id: z.string().optional(),
        data: z.record(z.string(), z.unknown()).optional(),
      }),
      input,
    );
    const data = action.data || {};
    if (action.type === "create-capsule") {
      const value = parse(capsuleInput, data);
      const id = randomUUID();
      await this.db.capsule.create({
        data: {
          ...value,
          id,
          slug: `${value.title
            .toLowerCase()
            .replace(/[^a-z0-9]+/g, "-")
            .slice(0, 60)}-${id.slice(0, 8)}`,
          ownerId: user.id,
        },
      });
    } else if (action.type === "profile") {
      const value = parse(
        z.object({
          name: z.string().trim().min(2).max(120),
          bio: z.string().max(2000),
          domain: z.string().max(120),
        }),
        data,
      );
      const updated = await this.db.user.update({
        where: { id: user.id },
        data: value,
      });
      return this.snapshot(updated);
    } else if (action.type === "revoke") {
      const grant = await this.db.licenseGrant.findUnique({
        where: { id: action.id || "" },
      });
      if (!grant || grant.ownerId !== user.id)
        throw new NotFoundException("Grant not found.");
      await this.db.$transaction([
        this.db.licenseGrant.update({
          where: { id: grant.id },
          data: { status: "REVOKED" },
        }),
        this.db.auditEvent.create({
          data: {
            actorId: user.id,
            capsuleId: grant.capsuleId,
            action: "license.revoked",
            entityId: grant.id,
            metadata: {},
          },
        }),
      ]);
    } else {
      const capsule = await this.owned(user, action.capsuleId || "");
      if (action.type === "add-source") {
        const value = parse(
          z.object({
            title: z.string().trim().min(2).max(180),
            text: z.string().trim().min(20).max(1000000),
            type: z.enum(["NOTE", "DOCUMENT", "CASE", "INTERVIEW"]),
          }),
          data,
        );
        const id = randomUUID();
        const objectKey = `${user.id}/${capsule.id}/${id}.txt`;
        const stored = await this.storage.putText(objectKey, value.text);
        await this.db.$transaction(async (tx) => {
          await tx.sourceAsset.create({
            data: {
              id,
              capsuleId: capsule.id,
              contributorId: user.id,
              contributor: user.name,
              ...value,
              ...stored,
              objectKey,
              contentType: "text/plain",
            },
          });
          await tx.jobRecord.create({
            data: {
              id: `source-${id}`,
              capsuleId: capsule.id,
              kind: "source-process",
              payload: { sourceId: id },
            },
          });
          await tx.capsule.update({
            where: { id: capsule.id },
            data: { revision: { increment: 1 } },
          });
        });
        await this.jobs.enqueue(id, capsule.id);
      } else if (["approve", "reject", "edit"].includes(action.type)) {
        const item = await this.db.knowledgeItem.findFirst({
          where: { id: action.id, capsuleId: capsule.id },
        });
        if (!item) throw new NotFoundException("Knowledge item not found.");
        const patch =
          action.type === "edit"
            ? {
                text: parse(z.string().trim().min(10).max(10000), data.text),
                status: "PENDING",
              }
            : { status: action.type === "approve" ? "APPROVED" : "REJECTED" };
        await this.db.$transaction([
          this.db.knowledgeItem.update({ where: { id: item.id }, data: patch }),
          this.db.capsule.update({
            where: { id: capsule.id },
            data: { revision: { increment: 1 } },
          }),
          this.db.auditEvent.create({
            data: {
              actorId: user.id,
              capsuleId: capsule.id,
              action: `knowledge.${action.type}`,
              entityId: item.id,
              metadata: {
                previousHash: hash(item.text),
                newHash: hash(patch.text ?? item.text),
              },
            },
          }),
        ]);
      } else if (action.type === "evaluate") {
        const items = await this.db.knowledgeItem.findMany({
          where: { capsuleId: capsule.id, status: "APPROVED" },
        });
        if (!items.length)
          throw new BadRequestException(
            "Approve knowledge before running an evaluation.",
          );
        const result = await ai<{
          passed: boolean;
          metrics: unknown[];
          cases: unknown[];
          suiteHash: string;
        }>("evals/run", {
          capsuleId: capsule.id,
          knowledge: items,
          goldenCases: await this.db.evaluationCase.findMany({where:{capsuleId:capsule.id}}),
          idempotencyKey: `eval-${capsule.id}-${capsule.revision}`,
          allowedScope: { knowledgeIds: items.map((i) => i.id) },
        });
        await this.db.evaluation.create({
          data: {
            capsuleId: capsule.id,
            revision: capsule.revision,
            passed: result.passed,
            metrics: json(result.metrics),
            cases: json(result.cases),
            suiteHash: result.suiteHash,
          },
        });
      } else if (action.type === "publish") {
        await this.db.$transaction(
          async (tx) => {
            const current = await tx.capsule.findUniqueOrThrow({
              where: { id: capsule.id },
            });
            const evaluation = await tx.evaluation.findFirst({
              where: {
                capsuleId: capsule.id,
                revision: current.revision,
                passed: true,
              },
              orderBy: { createdAt: "desc" },
            });
            if (!evaluation)
              throw new BadRequestException(
                "A passing evaluation for the current knowledge revision is required.",
              );
            if (
              await tx.knowledgeItem.count({
                where: {
                  capsuleId: capsule.id,
                  critical: true,
                  status: "PENDING",
                },
              })
            )
              throw new BadRequestException(
                "Resolve critical conflicts first.",
              );
            const knowledge = await tx.knowledgeItem.findMany({
              where: { capsuleId: capsule.id, status: "APPROVED" },
              orderBy: { id: "asc" },
            });
            if (!knowledge.length)
              throw new BadRequestException("No approved knowledge.");
            const count = await tx.capsuleVersion.count({
              where: { capsuleId: capsule.id },
            });
            const version = `1.${count}.0`;
            const sources = await tx.sourceAsset.findMany({
              where: { id: { in: knowledge.map((k) => k.sourceId) } },
            });
            const manifest = {
              schemaVersion: "1.0",
              capsuleId: capsule.id,
              version,
              contributors: [...new Set(knowledge.map((k) => k.contributorId))]
                .sort()
                .map((expertRef) => ({
                  expertRef,
                  contributionHash: hash(
                    canonical(
                      knowledge.filter((k) => k.contributorId === expertRef),
                    ),
                  ),
                })),
              approvedKnowledge: knowledge.map((k) => k.id),
              sourceHashes: sources.map((s) => s.sha256).sort(),
              evaluationSuiteHash: evaluation.suiteHash,
              licensePolicyHash: hash(
                canonical(
                  await tx.licenseTemplate.findMany({
                    where: { capsuleId: capsule.id },
                    orderBy: { id: "asc" },
                  }),
                ),
              ),
              createdAt: new Date().toISOString(),
            };
            const manifestHash = hash(canonical(manifest));
            await tx.capsuleVersion.create({
              data: {
                capsuleId: capsule.id,
                version,
                manifestHash,
                manifest: json(manifest),
                knowledge: json(knowledge),
                evaluation: json(evaluation),
              },
            });
            await tx.capsule.update({
              where: { id: capsule.id },
              data: { status: "PUBLISHED", currentVersion: version },
            });
            await tx.jobRecord.create({
              data: {
                id: `anchor-${manifestHash}`,
                capsuleId: capsule.id,
                kind: "stellar-publish",
                payload: {
                  manifestHash,
                  version,
                  capsuleId: capsule.id,
                  evaluationHash: evaluation.suiteHash,
                },
                status: "PENDING",
              },
            });
          },
          { isolationLevel: Prisma.TransactionIsolationLevel.Serializable },
        );
      } else if (action.type === "grant") {
        if (!capsule.currentVersion)
          throw new BadRequestException(
            "Publish a capsule before granting access.",
          );
        const value = parse(grantInput, data);
        await this.db.$transaction(async (tx) => {
          const template = await tx.licenseTemplate.create({
            data: {
              capsuleId: capsule.id,
              name: value.name,
              audience: value.audience,
              purposes: value.purposes,
              aiTrainingAllowed: value.aiTrainingAllowed,
              commercialUse: value.commercialUse,
              derivativeUse: value.derivativeUse,
              usageLimit: value.usageLimit,
              durationDays: value.days,
            },
          });
          await tx.licenseGrant.create({
            data: {
              templateId: template.id,
              capsuleId: capsule.id,
              ownerId: user.id,
              name: value.name,
              grantee: value.grantee,
              audience: value.audience,
              purposes: value.purposes,
              aiTrainingAllowed: value.aiTrainingAllowed,
              commercialUse: value.commercialUse,
              derivativeUse: value.derivativeUse,
              usageLimit: value.usageLimit,
              expiresAt: new Date(Date.now() + value.days * 86400000),
            },
          });
        });
      } else throw new ForbiddenException("Unsupported workspace action.");
    }
    return this.snapshot(user);
  }
}
