import {
  Body,
  Controller,
  Post,
  Param,
  Req,
  NotFoundException,
  BadRequestException,
} from "@nestjs/common";
import { ApiTags } from "@nestjs/swagger";
import type { FastifyRequest } from "fastify";
import { randomUUID } from "node:crypto";
import { z } from "zod";
import { Database } from "./database";
import { Storage } from "./storage";
import { Jobs } from "./jobs";
import { ProductController } from "./controller";
import { WorkspaceService } from "./workspace";
import { parse, hash, ai } from "./core";
@ApiTags("Capture")
@Controller("v1/capsules/:id")
export class CaptureController {
  @Post('evaluations/cases') async addCase(@Param('id') id:string,@Req() req:FastifyRequest,@Body() body:unknown){const user=await this.auth.user(req);await this.workspace.owned(user,id);const value=parse(z.object({question:z.string().trim().min(5).max(4000),expectedElements:z.array(z.string().min(1).max(2000)).max(30).default([]),forbiddenElements:z.array(z.string().min(1).max(2000)).max(30).default([]),unsupported:z.boolean().default(false)}).refine(v=>v.unsupported||v.expectedElements.length>0,'Supported cases need expected answer elements.'),body);return this.db.$transaction(async tx=>{const result=await tx.evaluationCase.create({data:{...value,capsuleId:id,approvedBy:user.id}});await tx.capsule.update({where:{id},data:{revision:{increment:1}}});return result;});}
  constructor(
    private db: Database,
    private storage: Storage,
    private jobs: Jobs,
    private auth: ProductController,
    private workspace: WorkspaceService,
  ) {}
  @Post("sources/upload-url") async upload(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.auth.user(req);
    await this.workspace.owned(user, id);
    const value = parse(
      z.object({
        contentType: z.string().min(1).max(100),
        size: z
          .number()
          .int()
          .positive()
          .max(100 * 1024 * 1024),
        sha256: z.string().regex(/^[a-f0-9]{64}$/),
        idempotencyKey: z.string().min(1).max(200),
        interviewId: z.string().uuid().optional(),
        sequence: z.number().int().min(0).optional(),
      }),
      body,
    );
    if (
      value.interviewId &&
      !(await this.db.interview.findFirst({
        where: { id: value.interviewId, capsuleId: id, ownerId: user.id },
      }))
    )
      throw new NotFoundException();
    const key = hash(`${user.id}:${value.idempotencyKey}`);
    const existing = await this.db.uploadTicket.findUnique({
      where: { idempotencyKey: key },
    });
    if (
      existing &&
      (existing.capsuleId !== id ||
        existing.sha256 !== value.sha256 ||
        existing.size !== value.size)
    )
      throw new BadRequestException(
        "Upload key already used for a different source.",
      );
    const ticket =
      existing ||
      (await this.db.uploadTicket.create({
        data: {
          ...value,
          idempotencyKey: key,
          ownerId: user.id,
          capsuleId: id,
          objectKey: `${user.id}/${id}/${randomUUID()}`,
          expiresAt: new Date(Date.now() + 300000),
        },
      }));
    return {
      objectKey: ticket.objectKey,
      url: await this.storage.uploadUrl(ticket.objectKey, ticket.contentType),
    };
  }
  @Post("sources/finalize") async finalize(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.auth.user(req);
    await this.workspace.owned(user, id);
    const value = parse(
      z.object({
        objectKey: z.string().max(300),
        title: z.string().trim().min(1).max(180),
        sha256: z.string().regex(/^[a-f0-9]{64}$/),
      }),
      body,
    );
    const ticket = await this.db.uploadTicket.findFirst({
      where: { objectKey: value.objectKey, capsuleId: id, ownerId: user.id },
    });
    if (!ticket || ticket.sha256 !== value.sha256)
      throw new NotFoundException("Upload not found.");
    const existing = await this.db.sourceAsset.findUnique({
      where: { objectKey: ticket.objectKey },
    });
    if (existing) return { id: existing.id, status: existing.status };
    const bytes = await this.storage.read(ticket.objectKey);
    if (bytes.length !== ticket.size || hash(bytes) !== ticket.sha256)
      throw new BadRequestException(
        "Uploaded source failed integrity validation.",
      );
    const source = await this.db.sourceAsset.create({
      data: {
        capsuleId: id,
        contributorId: user.id,
        contributor: user.name,
        title: value.title,
        type: ticket.contentType.startsWith("audio/")
          ? "AUDIO"
          : ticket.contentType.startsWith("video/")
            ? "VIDEO"
            : "DOCUMENT",
        objectKey: ticket.objectKey,
        contentType: ticket.contentType,
        size: ticket.size,
        sha256: ticket.sha256,
      },
    });
    await this.jobs.enqueue(source.id, id);
    return { id: source.id, status: source.status };
  }
  @Post("interviews") async interview(
    @Param("id") id: string,
    @Req() req: FastifyRequest,
  ) {
    const user = await this.auth.user(req);
    await this.workspace.owned(user, id);
    return this.db.interview.create({
      data: { capsuleId: id, ownerId: user.id, coverage: [] },
    });
  }
  @Post("interviews/:interviewId/segments") async segment(
    @Param("id") id: string,
    @Param("interviewId") interviewId: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.auth.user(req);
    await this.workspace.owned(user, id);
    const interview = await this.db.interview.findFirst({
      where: { id: interviewId, capsuleId: id, ownerId: user.id },
    });
    if (!interview) throw new NotFoundException();
    const value = parse(
      z.object({
        objectKey: z.string().max(300),
        sequence: z.number().int().nonnegative(),
        sha256: z.string().regex(/^[a-f0-9]{64}$/),
      }),
      body,
    );
    const ticket = await this.db.uploadTicket.findFirst({
      where: {
        objectKey: value.objectKey,
        ownerId: user.id,
        capsuleId: id,
        interviewId,
        sequence: value.sequence,
      },
    });
    if (!ticket || ticket.sha256 !== value.sha256)
      throw new BadRequestException(
        "Audio upload does not match this interview.",
      );
    const bytes = await this.storage.read(ticket.objectKey);
    if (hash(bytes) !== value.sha256 || bytes.length !== ticket.size)
      throw new BadRequestException("Audio integrity validation failed.");
    const created = await this.db.interviewSegment.upsert({
      where: {
        interviewId_sequence: { interviewId, sequence: value.sequence },
      },
      create: { interviewId, ...value, contentType: ticket.contentType },
      update: {},
    });
    await this.jobs.enqueueTranscription(created.id, interviewId, id, value.sequence);
    return created;
  }
  @Post("interviews/:interviewId/complete") async complete(
    @Param("id") id: string,
    @Param("interviewId") interviewId: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.auth.user(req);
    await this.workspace.owned(user, id);
    const { segments } = parse(
      z.object({ segments: z.number().int().min(1).max(10000) }),
      body,
    );
    const interview = await this.db.interview.findFirst({
      where: { id: interviewId, capsuleId: id, ownerId: user.id },
    });
    if (!interview) throw new NotFoundException();
    const chunks = await this.db.interviewSegment.findMany({
      where: { interviewId },
      orderBy: { sequence: "asc" },
    });
    if (
      chunks.length !== segments ||
      chunks.some((chunk, index) => chunk.sequence !== index)
    )
      throw new BadRequestException(
        "Some audio chunks are missing. Retry pending uploads.",
      );
    await this.db.interview.update({
      where: { id: interviewId },
      data: { status: "CAPTURED" },
    });
    // Re-enqueue transcription for any segment a prior enqueue may have
    // missed (e.g. a crash between upload and enqueue) — idempotent, since
    // the worker skips a segment that already has transcript text.
    for (const chunk of chunks)
      if (!chunk.text)
        await this.jobs.enqueueTranscription(chunk.id, interviewId, id, chunk.sequence);
    const transcribed = chunks.every((c) => c.text);
    return {
      id: interviewId,
      status: "CAPTURED",
      segments: chunks.length,
      transcriptionStatus: transcribed ? "COMPLETE" : "PENDING",
    };
  }
  @Post("interviews/:interviewId/next-question") async followup(
    @Param("id") id: string,
    @Param("interviewId") interviewId: string,
    @Req() req: FastifyRequest,
    @Body() body: unknown,
  ) {
    const user = await this.auth.user(req);
    await this.workspace.owned(user, id);
    const interview = await this.db.interview.findFirst({
      where: { id: interviewId, capsuleId: id, ownerId: user.id },
    });
    if (!interview) throw new NotFoundException();
    const { transcript } = parse(
      z.object({ transcript: z.string().max(100000) }),
      body,
    );
    const result = await ai<{ question: string; coverage: string[] }>(
      "interview/next",
      {
        capsuleId: id,
        transcript,
        coverage: interview.coverage,
        allowedScope: { interviewIds: [interviewId] },
        idempotencyKey: hash(`${interviewId}:${transcript}`),
      },
    );
    await this.db.interview.update({
      where: { id: interviewId },
      data: { coverage: result.coverage },
    });
    return result;
  }
}
