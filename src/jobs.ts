import {
  Injectable,
  OnModuleDestroy,
  OnModuleInit,
  Logger,
} from "@nestjs/common";
import { Queue, Worker } from "bullmq";
import { Database } from "./database";
import { Storage } from "./storage";
import { ai, hash } from "./core";
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
@Injectable()
export class Jobs implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(Jobs.name);
  private queue: Queue;
  private worker?: Worker;
  private reconcile?: NodeJS.Timeout;
  constructor(
    private db: Database,
    private storage: Storage,
  ) {
    const url = new URL(process.env.REDIS_URL || "redis://localhost:6381");
    this.queue = new Queue("synapse-source-process", {
      connection: { host: url.hostname, port: Number(url.port || 6379) },
      defaultJobOptions: {
        attempts: 3,
        backoff: { type: "exponential", delay: 2000 },
        removeOnComplete: 1000,
        removeOnFail: false,
      },
    });
  }
  async onModuleInit() {
    const url = new URL(process.env.REDIS_URL || "redis://localhost:6381");
    this.worker = new Worker(
      "synapse-source-process",
      async (job) => {
        await this.process(job.id!, job.data.sourceId);
      },
      {
        connection: { host: url.hostname, port: Number(url.port || 6379) },
        concurrency: 2,
      },
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
    this.reconcile = setInterval(
      () =>
        void this.requeue().catch(() =>
          this.logger.warn("Outbox reconciliation deferred"),
        ),
      15000,
    );
    await this.requeue();
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
  private async requeue() {
    const pending = await this.db.jobRecord.findMany({
      where: { kind: "source-process", status: "PENDING" },
      take: 100,
    });
    for (const job of pending) {
      const sourceId = (job.payload as { sourceId: string }).sourceId;
      await this.queue.add("source-process", { sourceId }, { jobId: job.id });
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
  async onModuleDestroy() {
    if (this.reconcile) clearInterval(this.reconcile);
    await this.worker?.close();
    await this.queue.close();
  }
}
