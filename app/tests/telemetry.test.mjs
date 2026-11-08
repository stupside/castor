import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:http";
import test from "node:test";
import { context, trace } from "@opentelemetry/api";
import { register } from "../instrumentation.ts";
import { proxyWatch } from "../src/lib/watch-proxy.ts";

test("initialization exports traces and propagates the active trace through Watch", async (t) => {
  const exports = [];
  let parent;
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    if (req.url === "/otel/v1/traces")
      exports.push(JSON.parse(Buffer.concat(chunks).toString()));
    else parent = req.headers.traceparent;
    res.setHeader("Content-Type", "application/json");
    res.end("{}");
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const url = `http://127.0.0.1:${server.address().port}`;
  process.env.OTEL_EXPORTER_OTLP_ENDPOINT = `${url}/otel`;
  process.env.OTEL_EXPORTER_OTLP_PROTOCOL = "http/json";
  register();
  const span = trace.getTracer("castor-test").startSpan("watch-test");
  await context.with(trace.setSpan(context.active(), span), async () => {
    const response = await proxyWatch(
      { name: "Watch", parent: { typeName: "castor.v1.CastService" } },
      new Request(`${url}/watch`, { method: "POST" }),
      { url, token: "" },
    );
    await response.text();
  });
  span.end();
  await trace.getTracerProvider().getDelegate().forceFlush();
  assert.equal(
    parent,
    `00-${span.spanContext().traceId}-${span.spanContext().spanId}-01`,
  );
  assert.equal(exports.length, 1);
  assert.equal(
    exports[0].resourceSpans[0].scopeSpans[0].spans[0].name,
    "watch-test",
  );
});
