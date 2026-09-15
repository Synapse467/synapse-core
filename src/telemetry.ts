import { randomBytes } from "node:crypto";
import type { FastifyInstance, FastifyRequest } from "fastify";

declare module "fastify" {
  interface FastifyRequest {
    requestId: string;
  }
}

/**
 * OpenTelemetry-compatible request correlation (PRD observability).
 * Always attaches `x-request-id`. If `OTEL_EXPORTER_OTLP_ENDPOINT` is set,
 * posts a completed-span JSON payload to that collector; otherwise spans
 * stay local as structured logs. Never fabricates traces for missing work.
 */
export function requestId(req: FastifyRequest): string {
  const existing = req.headers["x-request-id"];
  if (typeof existing === "string" && existing.length > 0 && existing.length < 128)
    return existing;
  return randomBytes(12).toString("hex");
}

export async function registerTelemetry(app: FastifyInstance) {
  app.addHook("onRequest", async (req, reply) => {
    req.requestId = requestId(req);
    reply.header("x-request-id", req.requestId);
  });
  app.addHook("onResponse", async (req, reply) => {
    const route =
      (req as FastifyRequest & { routeOptions?: { url?: string } }).routeOptions
        ?.url || req.url;
    const span = {
      name: `${req.method} ${route}`,
      attributes: {
        "http.method": req.method,
        "http.status_code": reply.statusCode,
        "http.route": route,
      },
    };
    if (process.env.OTEL_EXPORTER_OTLP_ENDPOINT) {
      const url = `${process.env.OTEL_EXPORTER_OTLP_ENDPOINT.replace(/\/$/, "")}/v1/traces`;
      const headers: Record<string, string> = { "content-type": "application/json" };
      for (const pair of (process.env.OTEL_EXPORTER_OTLP_HEADERS || "").split(",")) {
        if (!pair.includes("=")) continue;
        const [k, ...rest] = pair.split("=");
        headers[k.trim()] = rest.join("=").trim();
      }
      void fetch(url, {
        method: "POST",
        headers,
        body: JSON.stringify({
          resourceSpans: [
            {
              resource: { attributes: [{ key: "service.name", value: { stringValue: "synapse-api" } }] },
              scopeSpans: [{ spans: [{ name: span.name, attributes: Object.entries(span.attributes).map(([key, value]) => ({ key, value: { stringValue: String(value) } })) }] }],
            },
          ],
        }),
      }).catch(() => undefined);
    }
  });
}
