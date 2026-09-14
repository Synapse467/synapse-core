import "dotenv/config";
import "reflect-metadata";
import { Module } from "@nestjs/common";
import { NestFactory } from "@nestjs/core";
import {
  FastifyAdapter,
  NestFastifyApplication,
} from "@nestjs/platform-fastify";
import { SwaggerModule, DocumentBuilder } from "@nestjs/swagger";
import cookie from "@fastify/cookie";
import rateLimit from "@fastify/rate-limit";
import { Database } from "./database";
import { Storage } from "./storage";
import { Jobs } from "./jobs";
import { WorkspaceService } from "./workspace";
import { ProductController } from "./controller";
import { CaptureController } from "./capture";
@Module({
  controllers: [ProductController, CaptureController],
  providers: [Database, Storage, Jobs, WorkspaceService, ProductController],
})
class AppModule {}
async function main() {
  const app = await NestFactory.create<NestFastifyApplication>(
    AppModule,
    new FastifyAdapter({
      bodyLimit: 2 * 1024 * 1024,
      logger: {
        redact: [
          "req.headers.cookie",
          "req.headers.authorization",
          "req.body",
          "res.headers.set-cookie",
        ],
      },
    }),
  );
  await app.register(cookie);
  await app.register(rateLimit, { max: 120, timeWindow: "1 minute" });
  const origin = process.env.WEB_ORIGIN || "http://localhost:3000";
  app.enableCors({ origin, credentials: true });
  app
    .getHttpAdapter()
    .getInstance()
    .addHook("onRequest", async (req, reply) => {
      if (
        !["GET", "HEAD", "OPTIONS"].includes(req.method) &&
        req.headers.origin !== origin
      ) {
        reply
          .code(403)
          .send({ message: "A trusted application origin is required." });
      }
    });
  const document = SwaggerModule.createDocument(
    app,
    new DocumentBuilder()
      .setTitle("Synapse API")
      .setVersion("1.0")
      .addCookieAuth("synapse_session")
      .build(),
  );
  document.openapi = "3.1.0";
  SwaggerModule.setup("v1/docs", app, document);
  app.enableShutdownHooks();
  await app.listen(Number(process.env.PORT || 4000), "127.0.0.1");
}
main().catch(() => {
  process.stderr.write(
    "Synapse API could not start. Check database, queue, and environment configuration.\n",
  );
  process.exitCode = 1;
});
