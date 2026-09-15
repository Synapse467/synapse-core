import {
  Injectable,
  OnModuleDestroy,
  OnModuleInit,
  Logger,
} from "@nestjs/common";
import { Queue, Worker } from "bullmq";
import { randomUUID } from "node:crypto";
import { Database } from "./database";
import { Storage } from "./storage";
import { StellarService } from "./stellar";
import { ai, hash, scanContent, scanUploadedFile } from "./core";
import {
  contributorSharePlan,
  usageManifestHash,
  usagePeriod,
} from "./billing";
import { z } from "zod";
const extraction = z.object({
  items: z.array(
    z.object({
      kind: z.enum(["CLAIM", "PROCEDURE", "HEURISTIC", "EXCEPTION", "CASE"]),
      text: z.string().min(1),
      confidence: z.number().min(0).max(1),
      quote: z.string().min(1),
      segmentRef: z.string(),
      critical: z.boolean().optional(),
    }),
  ),
});

function redisConnection() {
  const url = new URL(process.env.REDIS_URL || "redis://localhost:6381");
  return { host: url.hostname, port: Number(url.port || 6379) };
}

@Injectable()
export class Jobs implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(Jobs.name);
  private queue: Queue;
  private stellarQueue: Queue;
  private transcribeQueue: Queue;
  private worker?: Worker;
  private stellarWorker?: Worker;
  private transcribeWorker?: Worker;
  private reconcile?: NodeJS.Timeout;
  private usageBatchTimer?: NodeJS.Timeout;
  private settlementTimer?: NodeJS.Timeout;
  constructor(
    private db: Database,
    private storage: Storage,
    private stellar: StellarService,
  ) {
    this.queue = new Queue("synapse-source-process", {
      connection: redisConnection(),
      defaultJobOptions: {
        attempts: 3,
        backoff: { type: "exponential", delay: 2000 },
        removeOnComplete: 1000,
        removeOnFail: false,
      },
    });
    this.stellarQueue = new Queue("synapse-stellar-publish", {
      connection: redisConnection(),
      defaultJobOptions: {
        attempts: 5,
        backoff: { type: "exponential", delay: 5000 },
        removeOnComplete: 1000,
        removeOnFail: false,
      },
    });
    this.transcribeQueue = new Queue("synapse-transcribe", {
      connection: redisConnection(),
      defaultJobOptions: {
        attempts: 3,
        backoff: { type: "exponential", delay: 2000 },
        removeOnComplete: 1000,
        removeOnFail: false,
      },
    });
  }
  async onModuleInit() {
    const connection = redisConnection();
    this.worker = new Worker(
      "synapse-source-process",
      async (job) => {
        await this.process(job.id!, job.data.sourceId);
      },
      { connection, concurrency: 2 },
    );
    this.worker.on("failed", async (job) => {
      if (job)
        await this.db.jobRecord.update({
          where: { id: job.id },
          data: {
            status: job.attemptsMade >= 3 ? "DEAD_LETTER" : "RETRYING",
            attempts: job.attemptsMade,
            lastError:
              "Source processing failed. Retry after checking the AI/storage services.",
          },
        });
    });
    this.worker.on("error", () =>
      this.logger.error("Source worker connection failure"),
    );

    this.stellarWorker = new Worker(
      "synapse-stellar-publish",
      async (job) => {
        await this.anchorJob(job.id!);
      },
      { connection, concurrency: 1 },
    );
    this.stellarWorker.on("failed", async (job) => {
      if (job)
        await this.db.jobRecord.update({
          where: { id: job.id },
          data: {
            status: job.attemptsMade >= 5 ? "DEAD_LETTER" : "RETRYING",
            attempts: job.attemptsMade,
            lastError: String(job.failedReason || "Stellar anchoring failed."),
          },
        });
    });
    this.stellarWorker.on("error", () =>
      this.logger.error("Stellar worker connection failure"),
    );

    this.transcribeWorker = new Worker(
      "synapse-transcribe",
      async (job) => {
        await this.transcribeSegment(job.id!);
      },
      { connection, concurrency: 2 },
    );
    this.transcribeWorker.on("failed", async (job) => {
      if (job)
        await this.db.jobRecord.update({
          where: { id: job.id },
          data: {
            status: job.attemptsMade >= 3 ? "DEAD_LETTER" : "RETRYING",
            attempts: job.attemptsMade,
            lastError: String(job.failedReason || "Transcription failed."),
          },
        });
    });
    this.transcribeWorker.on("error", () =>
      this.logger.error("Transcribe worker connection failure"),
    );

    this.reconcile = setInterval(
      () =>
        void this.requeue().catch(() =>
          this.logger.warn("Outbox reconciliation deferred"),
        ),
      15000,
    );
    await this.requeue();
    const usageMs = Number(process.env.USAGE_BATCH_INTERVAL_MS || 60000);
    const settleMs = Number(process.env.SETTLEMENT_INTERVAL_MS || 60000);
    this.usageBatchTimer = setInterval(
      () =>
        void this.batchUsageReceipts().catch((err) =>
          this.logger.warn(`Usage-receipt batch deferred: ${String(err)}`),
        ),
      Math.max(15000, usageMs),
    );
    this.settlementTimer = setInterval(
      () =>
        void this.settlePricedUsage().catch((err) =>
          this.logger.warn(`Settlement batch deferred: ${String(err)}`),
        ),
      Math.max(15000, settleMs),
    );
  }
  async enqueue(sourceId: string, capsuleId: string) {
    const id = `source-${sourceId}`;
    await this.db.jobRecord.upsert({
      where: { id },
      create: { id, capsuleId, kind: "source-process", payload: { sourceId } },
      update: {},
    });
    await this.queue.add("source-process", { sourceId }, { jobId: id });
  }
  /** Queues a real on-chain Stellar anchor job. The JobRecord row (kind: stellar-publish | license-anchor | license-revoke) must already exist. */
  async enqueueStellarJob(jobId: string) {
    await this.stellarQueue.add("stellar-anchor", {}, { jobId });
  }
  /** Queues real AI transcription of one recorded interview audio segment. */
  async enqueueTranscription(
    segmentId: string,
    interviewId: string,
    capsuleId: string,
    sequence: number,
  ) {
    const id = `transcribe-${interviewId}-${sequence}`;
    await this.db.jobRecord.upsert({
      where: { id },
      create: {
        id,
        capsuleId,
        kind: "interview-transcribe",
        payload: { segmentId, interviewId },
      },
      update: {},
    });
    await this.transcribeQueue.add("transcribe", {}, { jobId: id });
  }
  private async requeue() {
    const pendingSource = await this.db.jobRecord.findMany({
      where: { kind: "source-process", status: "PENDING" },
      take: 100,
    });
    for (const job of pendingSource) {
      const sourceId = (job.payload as { sourceId: string }).sourceId;
      await this.queue.add("source-process", { sourceId }, { jobId: job.id });
    }
    const pendingTranscriptions = await this.db.jobRecord.findMany({
      where: { kind: "interview-transcribe", status: { in: ["PENDING", "RETRYING"] } },
      take: 100,
    });
    for (const job of pendingTranscriptions) {
      await this.transcribeQueue.add("transcribe", {}, { jobId: job.id });
    }
    const pendingAnchors = await this.db.jobRecord.findMany({
      where: {
        kind: {
          in: [
            "stellar-publish",
            "license-anchor",
            "license-revoke",
            "usage-batch",
            "settlement",
          ],
        },
        status: { in: ["PENDING", "RETRYING"] },
      },
      take: 100,
    });
    for (const job of pendingAnchors) {
      await this.stellarQueue.add("stellar-anchor", {}, { jobId: job.id });
    }
  }
  private async process(id: string, sourceId: string) {
    const record = await this.db.jobRecord.findUnique({ where: { id } });
    if (record?.status === "COMPLETED") return;
    await this.db.jobRecord.update({
      where: { id },
      data: { status: "RUNNING", attempts: { increment: 1 } },
    });
    const source = await this.db.sourceAsset.findUniqueOrThrow({
      where: { id: sourceId },
    });
    let text = source.text;
    if (!text) {
      const bytes = await this.storage.read(source.objectKey);
      if (hash(bytes) !== source.sha256)
        throw new Error("Source integrity mismatch");
      const binaryScan = await scanUploadedFile(bytes, source.title);
      if (!binaryScan.safe) {
        await this.db.$transaction(async (tx) => {
          const existing = await tx.jobRecord.findUnique({ where: { id } });
          if (existing?.status === "COMPLETED") return;
          await tx.sourceAsset.update({
            where: { id: sourceId },
            data: { status: "FLAGGED" },
          });
          await tx.jobRecord.update({
            where: { id },
            data: { status: "COMPLETED", lastError: binaryScan.reason },
          });
          await tx.auditEvent.create({
            data: {
              actorId: source.contributorId,
              capsuleId: source.capsuleId,
              action: "source.flagged",
              entityId: sourceId,
              metadata: { reason: binaryScan.reason },
            },
          });
        });
        return;
      }
      const parsed = await ai<{ text: string }>("document", {
        capsuleId: source.capsuleId,
        sourceId,
        contentType: source.contentType,
        filename: source.title,
        contentBase64: bytes.toString("base64"),
        idempotencyKey: id,
        allowedScope: { sourceIds: [sourceId] },
      });
      text = parsed.text;
    }
    // PRD §21 security: "prompt-injection hardening for uploaded sources" —
    // scan before this text is ever sent to the extraction model or
    // persisted/queued as reviewable knowledge. A flagged source is a
    // terminal, non-retryable outcome, not a transient job failure.
    const scan = scanContent(text);
    if (!scan.safe) {
      await this.db.$transaction(async (tx) => {
        const existing = await tx.jobRecord.findUnique({ where: { id } });
        if (existing?.status === "COMPLETED") return;
        await tx.sourceAsset.update({
          where: { id: sourceId },
          data: { text, status: "FLAGGED" },
        });
        await tx.jobRecord.update({
          where: { id },
          data: { status: "COMPLETED", lastError: scan.reason },
        });
        await tx.auditEvent.create({
          data: {
            actorId: source.contributorId,
            capsuleId: source.capsuleId,
            action: "source.flagged",
            entityId: sourceId,
            metadata: { reason: scan.reason },
          },
        });
      });
      this.logger.warn(
        JSON.stringify({ event: "source.flagged", jobId: id, capsuleId: source.capsuleId }),
      );
      return;
    }
    const result = extraction.parse(
      await ai("extract", {
        capsuleId: source.capsuleId,
        sourceId,
        text,
        idempotencyKey: id,
        allowedScope: { sourceIds: [sourceId] },
      }),
    );
    for (const item of result.items)
      if (!text.includes(item.quote))
        throw new Error("Extracted citation is not present in source");
    await this.db.$transaction(async (tx) => {
      const existing = await tx.jobRecord.findUnique({ where: { id } });
      if (existing?.status === "COMPLETED") return;
      await tx.knowledgeItem.createMany({
        data: result.items.map((item, index) => ({
          id: hash(`${sourceId}:${index}`),
          capsuleId: source.capsuleId,
          sourceId,
          contributorId: source.contributorId,
          contributor: source.contributor,
          kind: item.kind,
          text: item.text,
          proposal: item.text,
          confidence: item.confidence,
          critical: item.critical || false,
          citation: {
            sourceId,
            segmentRef: item.segmentRef,
            quoteHash: hash(item.quote),
            quote: item.quote,
          },
        })),
        skipDuplicates: true,
      });
      await tx.sourceAsset.update({
        where: { id: sourceId },
        data: { text, status: "PROCESSED" },
      });
      await tx.jobRecord.update({
        where: { id },
        data: { status: "COMPLETED", lastError: null },
      });
      await tx.auditEvent.create({
        data: {
          actorId: source.contributorId,
          capsuleId: source.capsuleId,
          action: "source.processed",
          entityId: sourceId,
          metadata: { items: result.items.length },
        },
      });
    });
    this.logger.log(
      JSON.stringify({
        event: "source.processed",
        jobId: id,
        capsuleId: source.capsuleId,
      }),
    );
  }
  /**
   * Real interview transcription (PRD §3 Expert journey step 6: "AI
   * transcribes"). Reads the recorded audio segment back from storage,
   * verifies its integrity, and sends it to the AI service's real
   * `/internal/v1/transcribe` endpoint. Once every segment of a captured
   * interview has been transcribed, the assembled transcript becomes a
   * normal SourceAsset and is routed through the same extraction pipeline
   * as an uploaded document — closing the loop from "recorded interview" to
   * "extracted, reviewable knowledge" for real, with no manual paste step.
   */
  private async transcribeSegment(jobId: string) {
    const record = await this.db.jobRecord.findUnique({ where: { id: jobId } });
    if (!record || record.status === "COMPLETED") return;
    const payload = record.payload as { segmentId: string; interviewId: string };
    const segment = await this.db.interviewSegment.findUnique({
      where: { id: payload.segmentId },
    });
    if (!segment) return;
    if (segment.text) {
      await this.db.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null },
      });
      return;
    }
    await this.db.jobRecord.update({
      where: { id: jobId },
      data: { status: "RUNNING", attempts: { increment: 1 } },
    });
    const bytes = await this.storage.read(segment.objectKey);
    if (hash(bytes) !== segment.sha256)
      throw new Error("Interview segment integrity mismatch");
    const interview = await this.db.interview.findUniqueOrThrow({
      where: { id: payload.interviewId },
    });
    const result = await ai<{ text: string }>("transcribe", {
      capsuleId: interview.capsuleId,
      contentBase64: bytes.toString("base64"),
      filename: `segment-${segment.sequence}`,
      contentType: segment.contentType,
      idempotencyKey: jobId,
      allowedScope: { interviewIds: [interview.id] },
      retention: process.env.TRANSCRIPTION_RETENTION || "none",
    });
    await this.db.$transaction(async (tx) => {
      const existing = await tx.jobRecord.findUnique({ where: { id: jobId } });
      if (existing?.status === "COMPLETED") return;
      await tx.interviewSegment.update({
        where: { id: segment.id },
        data: { text: result.text },
      });
      await tx.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null },
      });
    });
    await this.maybeAssembleInterview(payload.interviewId);
  }
  /** Once a CAPTURED interview's segments are all transcribed, turns the assembled transcript into a real SourceAsset and runs it through extraction — same pipeline as an uploaded document. */
  private async maybeAssembleInterview(interviewId: string) {
    const interview = await this.db.interview.findUnique({ where: { id: interviewId } });
    if (!interview || interview.status !== "CAPTURED") return;
    const segments = await this.db.interviewSegment.findMany({
      where: { interviewId },
      orderBy: { sequence: "asc" },
    });
    if (!segments.length || segments.some((s) => !s.text)) return;
    const owner = await this.db.user.findUniqueOrThrow({ where: { id: interview.ownerId } });
    const transcript = segments.map((s) => s.text).join("\n\n");
    const sourceId = randomUUID();
    const bytes = Buffer.from(transcript, "utf-8");
    await this.storage.putText(`${interview.ownerId}/${interview.capsuleId}/${sourceId}.txt`, transcript);
    await this.db.$transaction(async (tx) => {
      const already = await tx.interview.findUnique({ where: { id: interviewId } });
      if (already?.status !== "CAPTURED") return; // another worker already assembled it
      await tx.sourceAsset.create({
        data: {
          id: sourceId,
          capsuleId: interview.capsuleId,
          contributorId: interview.ownerId,
          contributor: owner.name,
          title: `Interview transcript (${new Date(interview.createdAt).toISOString().slice(0, 10)})`,
          type: "INTERVIEW",
          objectKey: `${interview.ownerId}/${interview.capsuleId}/${sourceId}.txt`,
          sha256: hash(bytes),
          contentType: "text/plain",
          size: bytes.length,
          text: transcript,
          status: "PENDING",
        },
      });
      await tx.jobRecord.create({
        data: {
          id: `source-${sourceId}`,
          capsuleId: interview.capsuleId,
          kind: "source-process",
          payload: { sourceId },
        },
      });
      await tx.interview.update({ where: { id: interviewId }, data: { status: "TRANSCRIBED" } });
    });
    await this.enqueue(sourceId, interview.capsuleId);
    this.logger.log(
      JSON.stringify({ event: "interview.transcribed", interviewId, sourceId }),
    );
  }
  private async anchorJob(jobId: string) {
    const record = await this.db.jobRecord.findUnique({ where: { id: jobId } });
    if (!record || record.status === "COMPLETED") return;
    await this.db.jobRecord.update({
      where: { id: jobId },
      data: { status: "RUNNING", attempts: { increment: 1 } },
    });
    if (record.kind === "stellar-publish") await this.anchorCapsulePublish(jobId, record);
    else if (record.kind === "license-anchor") await this.anchorLicenseGrant(jobId, record);
    else if (record.kind === "license-revoke") await this.anchorLicenseRevocation(jobId, record);
    else if (record.kind === "usage-batch") await this.anchorUsageBatch(jobId, record);
    else if (record.kind === "settlement") await this.anchorSettlement(jobId, record);
    else this.logger.warn(`Unknown Stellar job kind: ${record.kind}`);
  }
  private async anchorCapsulePublish(
    jobId: string,
    record: { capsuleId: string; payload: unknown },
  ) {
    const payload = record.payload as {
      manifestHash: string;
      version: string;
      capsuleId: string;
      evaluationHash: string;
    };
    // Idempotency guard (PRD "every job receives an idempotency key" /
    // "all asynchronous work is idempotent"): a BullMQ retry/redelivery of a
    // job that already succeeded on-chain must not call publish_version
    // again — the contract's own immutability check would reject it as
    // VersionAlreadyPublished, which is not a real failure.
    const already = await this.db.capsuleVersion.findFirst({
      where: { capsuleId: payload.capsuleId, version: payload.version },
    });
    if (already?.stellarTxHash) {
      await this.db.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null },
      });
      return;
    }
    const result = await this.stellar.anchorCapsuleVersion({
      capsuleId: payload.capsuleId,
      version: payload.version,
      manifestHash: payload.manifestHash,
      evaluationHash: payload.evaluationHash,
    });
    await this.db.$transaction(async (tx) => {
      const existing = await tx.jobRecord.findUnique({ where: { id: jobId } });
      if (existing?.status === "COMPLETED") return;
      await tx.capsuleVersion.updateMany({
        where: { capsuleId: payload.capsuleId, version: payload.version },
        data: {
          stellarTxHash: result.txHash,
          stellarAnchoredAt: new Date(result.anchoredAt),
          stellarLedger: result.ledgerSequence,
        },
      });
      await tx.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null, result: { ...result } },
      });
      await tx.auditEvent.create({
        data: {
          actorId: "system",
          capsuleId: payload.capsuleId,
          action: "stellar.anchored",
          entityId: payload.manifestHash,
          metadata: { txHash: result.txHash, ledger: result.ledgerSequence },
        },
      });
    });
    this.logger.log(
      JSON.stringify({
        event: "stellar.publish.completed",
        jobId,
        capsuleId: payload.capsuleId,
        txHash: result.txHash,
      }),
    );
  }
  private async anchorLicenseGrant(
    jobId: string,
    record: { capsuleId: string; payload: unknown },
  ) {
    const payload = record.payload as {
      licenseId: string;
      capsuleId: string;
      version: string;
      grantee: string;
      termsHash: string;
      startsAt: string;
      expiresAt: string;
    };
    const already = await this.db.licenseGrant.findUnique({
      where: { id: payload.licenseId },
    });
    if (already?.stellarTxHash) {
      await this.db.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null },
      });
      return;
    }
    const result = await this.stellar.anchorLicenseGrant({
      licenseId: payload.licenseId,
      capsuleId: payload.capsuleId,
      version: payload.version,
      grantee: payload.grantee,
      termsHash: payload.termsHash,
      startsAt: new Date(payload.startsAt),
      expiresAt: new Date(payload.expiresAt),
    });
    await this.db.$transaction(async (tx) => {
      const existing = await tx.jobRecord.findUnique({ where: { id: jobId } });
      if (existing?.status === "COMPLETED") return;
      await tx.licenseGrant.update({
        where: { id: payload.licenseId },
        data: { stellarTxHash: result.txHash, stellarAnchoredAt: new Date(result.anchoredAt) },
      });
      await tx.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null, result: { ...result } },
      });
    });
    this.logger.log(
      JSON.stringify({ event: "stellar.license.anchored", jobId, txHash: result.txHash }),
    );
  }
  private async anchorLicenseRevocation(
    jobId: string,
    record: { capsuleId: string; payload: unknown },
  ) {
    const payload = record.payload as { licenseId: string };
    const already = await this.db.licenseGrant.findUnique({
      where: { id: payload.licenseId },
    });
    if (already?.stellarRevokeTxHash) {
      await this.db.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null },
      });
      return;
    }
    const result = await this.stellar.anchorLicenseRevocation(payload.licenseId);
    await this.db.$transaction(async (tx) => {
      const existing = await tx.jobRecord.findUnique({ where: { id: jobId } });
      if (existing?.status === "COMPLETED") return;
      await tx.licenseGrant.update({
        where: { id: payload.licenseId },
        data: { stellarRevokeTxHash: result.txHash },
      });
      await tx.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null, result: { ...result } },
      });
    });
    this.logger.log(
      JSON.stringify({ event: "stellar.license.revoked", jobId, txHash: result.txHash }),
    );
  }

  /**
   * PRD §17/§19 `usage-batch`: group un-receipted usage events per license,
   * persist an opaque manifest (ids + units, never query text), then queue
   * the on-chain UsageReceiptRegistry.record call. Idempotent: a retry that
   * finds events already reserved to a batch is a no-op.
   */
  async batchUsageReceipts() {
    const pending = await this.db.usageEvent.findMany({
      where: { receiptBatchId: null },
      take: 500,
      orderBy: { occurredAt: "asc" },
    });
    const byLicense = new Map<string, typeof pending>();
    for (const event of pending) {
      const list = byLicense.get(event.licenseId) || [];
      list.push(event);
      byLicense.set(event.licenseId, list);
    }
    for (const [licenseId, events] of byLicense) {
      const period = usagePeriod(events[events.length - 1].occurredAt);
      const manifestHash = usageManifestHash({
        licenseId,
        period,
        events: events.map((e) => ({ id: e.id, units: e.units })),
      });
      const batchId = randomUUID();
      const reserved = await this.db.$transaction(async (tx) => {
        const stillOpen = await tx.usageEvent.findMany({
          where: { id: { in: events.map((e) => e.id) }, receiptBatchId: null },
        });
        if (!stillOpen.length) return null;
        await tx.usageReceiptBatch.create({
          data: {
            id: batchId,
            capsuleId: stillOpen[0].capsuleId,
            licenseId,
            eventCount: stillOpen.length,
            usageManifestHash: manifestHash,
          },
        });
        await tx.usageEvent.updateMany({
          where: { id: { in: stillOpen.map((e) => e.id) } },
          data: { receiptBatchId: batchId },
        });
        await tx.jobRecord.create({
          data: {
            id: `usage-batch-${batchId}`,
            capsuleId: stillOpen[0].capsuleId,
            kind: "usage-batch",
            payload: { batchId, licenseId, period, usageManifestHash: manifestHash },
            status: "PENDING",
          },
        });
        return stillOpen[0].capsuleId;
      });
      if (reserved) await this.enqueueStellarJob(`usage-batch-${batchId}`);
    }
  }

  /**
   * PRD §17/§19 `settlement`: priced usage (`pricePerUnitMinor > 0`) that
   * has not been settled is grouped per capsule, split among contributors
   * with Stellar wallets, recorded as SettlementEvent rows, then queued
   * for an on-chain settle_split. Free licenses (price 0) never settle.
   */
  async settlePricedUsage() {
    const priced = await this.db.usageEvent.findMany({
      where: { settled: false },
      take: 500,
      orderBy: { occurredAt: "asc" },
    });
    if (!priced.length) return;
    const licenses = await this.db.licenseGrant.findMany({
      where: { id: { in: [...new Set(priced.map((e) => e.licenseId))] } },
    });
    const licenseById = new Map(licenses.map((l) => [l.id, l]));
    const byCapsule = new Map<string, typeof priced>();
    for (const event of priced) {
      const grant = licenseById.get(event.licenseId);
      if (!grant || grant.pricePerUnitMinor <= 0) continue;
      const list = byCapsule.get(event.capsuleId) || [];
      list.push(event);
      byCapsule.set(event.capsuleId, list);
    }
    for (const [capsuleId, events] of byCapsule) {
      const totalMinor = events.reduce((sum, event) => {
        const grant = licenseById.get(event.licenseId)!;
        return sum + event.units * grant.pricePerUnitMinor;
      }, 0);
      if (totalMinor <= 0) continue;
      const assetCode = licenseById.get(events[0].licenseId)?.assetCode || "USD";
      const capsule = await this.db.capsule.findUnique({ where: { id: capsuleId } });
      if (!capsule) continue;
      const owner = await this.db.user.findUnique({ where: { id: capsule.ownerId } });
      const knowledge = await this.db.knowledgeItem.findMany({
        where: { capsuleId, status: "APPROVED" },
        select: { contributorId: true },
      });
      const contributorIds = [...new Set(knowledge.map((k) => k.contributorId))];
      const contributors = await this.db.user.findMany({
        where: { id: { in: contributorIds } },
        select: { id: true, stellarPublicKey: true },
      });
      const plan = contributorSharePlan(
        {
          contributorId: owner?.id || capsule.ownerId,
          stellarPublicKey: owner?.stellarPublicKey || null,
        },
        contributors.map((c) => ({
          contributorId: c.id,
          stellarPublicKey: c.stellarPublicKey,
        })),
      );
      const settlementRef = randomUUID();
      const canAnchor = plan.every((share) => share.recipient);
      await this.db.$transaction(async (tx) => {
        const stillOpen = await tx.usageEvent.findMany({
          where: { id: { in: events.map((e) => e.id) }, settled: false },
        });
        if (!stillOpen.length) return;
        await tx.usageEvent.updateMany({
          where: { id: { in: stillOpen.map((e) => e.id) } },
          data: { settled: true },
        });
        for (const share of plan) {
          const amount =
            share === plan[plan.length - 1]
              ? totalMinor -
                plan
                  .slice(0, -1)
                  .reduce(
                    (sum, item) =>
                      sum + Math.floor((totalMinor * item.shareBps) / 10000),
                    0,
                  )
              : Math.floor((totalMinor * share.shareBps) / 10000);
          await tx.settlementEvent.create({
            data: {
              settlementRef,
              capsuleId,
              contributorId: share.contributorId,
              amountMinor: amount,
              assetCode,
              status: canAnchor ? "PENDING" : "RECORDED_OFFCHAIN",
            },
          });
        }
        if (canAnchor) {
          await tx.jobRecord.create({
            data: {
              id: `settlement-${settlementRef}`,
              capsuleId,
              kind: "settlement",
              payload: {
                settlementRef,
                totalAmountMinor: totalMinor,
                shares: plan,
              },
              status: "PENDING",
            },
          });
        }
      });
      if (canAnchor) await this.enqueueStellarJob(`settlement-${settlementRef}`);
      this.logger.log(
        JSON.stringify({
          event: "settlement.prepared",
          capsuleId,
          settlementRef,
          totalMinor,
          onChain: canAnchor,
        }),
      );
    }
  }

  private async anchorUsageBatch(
    jobId: string,
    record: { capsuleId: string; payload: unknown },
  ) {
    const payload = record.payload as {
      batchId: string;
      licenseId: string;
      period: number;
      usageManifestHash: string;
    };
    const batch = await this.db.usageReceiptBatch.findUnique({
      where: { id: payload.batchId },
    });
    if (batch?.stellarTxHash) {
      await this.db.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null },
      });
      return;
    }
    const result = await this.stellar.anchorUsageReceipt({
      receiptId: payload.batchId,
      licenseId: payload.licenseId,
      usageManifestHash: payload.usageManifestHash,
      period: payload.period,
    });
    await this.db.$transaction(async (tx) => {
      const existing = await tx.jobRecord.findUnique({ where: { id: jobId } });
      if (existing?.status === "COMPLETED") return;
      await tx.usageReceiptBatch.update({
        where: { id: payload.batchId },
        data: {
          stellarTxHash: result.txHash,
          stellarAnchoredAt: new Date(result.anchoredAt),
        },
      });
      await tx.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null, result: { ...result } },
      });
    });
    this.logger.log(
      JSON.stringify({ event: "stellar.usage.receipt", jobId, txHash: result.txHash }),
    );
  }

  private async anchorSettlement(
    jobId: string,
    record: { capsuleId: string; payload: unknown },
  ) {
    const payload = record.payload as {
      settlementRef: string;
      totalAmountMinor: number;
      shares: Array<{
        contributorId: string;
        recipient: string | null;
        shareBps: number;
      }>;
    };
    const already = await this.db.settlementEvent.findFirst({
      where: { settlementRef: payload.settlementRef, stellarTxHash: { not: null } },
    });
    if (already) {
      await this.db.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null },
      });
      return;
    }
    const shares = payload.shares.filter(
      (s): s is { contributorId: string; recipient: string; shareBps: number } =>
        Boolean(s.recipient),
    );
    const result = await this.stellar.anchorSettlement({
      settlementRef: payload.settlementRef,
      totalAmountMinor: payload.totalAmountMinor,
      contributorShares: shares.map((s) => ({
        recipient: s.recipient,
        shareBps: s.shareBps,
      })),
    });
    await this.db.$transaction(async (tx) => {
      const existing = await tx.jobRecord.findUnique({ where: { id: jobId } });
      if (existing?.status === "COMPLETED") return;
      await tx.settlementEvent.updateMany({
        where: { settlementRef: payload.settlementRef },
        data: { status: "ANCHORED", stellarTxHash: result.txHash },
      });
      await tx.jobRecord.update({
        where: { id: jobId },
        data: { status: "COMPLETED", lastError: null, result: { ...result } },
      });
    });
    this.logger.log(
      JSON.stringify({
        event: "stellar.settlement.anchored",
        jobId,
        txHash: result.txHash,
      }),
    );
  }

  async onModuleDestroy() {
    if (this.reconcile) clearInterval(this.reconcile);
    if (this.usageBatchTimer) clearInterval(this.usageBatchTimer);
    if (this.settlementTimer) clearInterval(this.settlementTimer);
    await this.worker?.close();
    await this.stellarWorker?.close();
    await this.transcribeWorker?.close();
    await this.queue.close();
    await this.stellarQueue.close();
    await this.transcribeQueue.close();
  }
}
