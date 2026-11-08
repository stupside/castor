import { registerOTel } from "@vercel/otel";

export function register() {
  if (
    !process.env.OTEL_EXPORTER_OTLP_ENDPOINT &&
    !process.env.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
  )
    return;
  registerOTel({
    serviceName: process.env.OTEL_SERVICE_NAME || "castor-web",
    instrumentations: [],
    propagators: ["tracecontext"],
  });
}
