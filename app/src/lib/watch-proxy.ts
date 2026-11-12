import type { DescMethodServerStreaming } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { encodeEnvelope } from "@connectrpc/connect/protocol";
import {
  contentTypeStreamProto,
  createEndStreamSerialization,
  endStreamFlag,
} from "@connectrpc/connect/protocol-connect";
import { context, propagation } from "@opentelemetry/api";

const protocolHeaders = [
  "content-type",
  "connect-protocol-version",
  "connect-timeout-ms",
  "connect-content-encoding",
  "connect-accept-encoding",
];

function copyProtocolHeaders(from: Headers) {
  const headers = new Headers();
  for (const name of protocolHeaders) {
    const value = from.get(name);
    if (value !== null) headers.set(name, value);
  }
  return headers;
}

// A browser follows server streams through here; every other call runs in a server action.
export async function proxyWatch(
  method: DescMethodServerStreaming,
  req: Request,
  api: { url: string; token: string },
): Promise<Response> {
  const abort = new AbortController();
  const cancel = () => abort.abort(req.signal.reason);
  req.signal.addEventListener("abort", cancel, { once: true });
  if (req.signal.aborted) cancel();
  const release = () => req.signal.removeEventListener("abort", cancel);
  const headers = copyProtocolHeaders(req.headers);
  propagation.inject(context.active(), headers, {
    set: (carrier, key, value) => carrier.set(key, value),
  });
  // Fetch decodes HTTP compression; never advertise the browser's encodings or relay its credentials.
  headers.set("accept-encoding", "identity");
  if (api.token) headers.set("authorization", `Bearer ${api.token}`);

  try {
    const init: RequestInit & { duplex: "half" } = {
      method: "POST",
      headers,
      body: req.body,
      duplex: "half",
      signal: abort.signal,
      cache: "no-store",
      redirect: "error",
    };
    const upstream = await fetch(
      `${api.url.replace(/\/+$/, "")}/${method.parent.typeName}/${method.name}`,
      init,
    );
    const responseHeaders = copyProtocolHeaders(upstream.headers);
    responseHeaders.set("cache-control", "no-store");
    responseHeaders.set("x-accel-buffering", "no");
    if (!upstream.body) {
      release();
      return new Response(null, {
        status: upstream.status,
        headers: responseHeaders,
      });
    }
    const reader = upstream.body.getReader();
    const body = new ReadableStream<Uint8Array>({
      async pull(controller) {
        try {
          const { done, value } = await reader.read();
          if (done) {
            release();
            controller.close();
          } else controller.enqueue(value);
        } catch (e) {
          release();
          controller.error(e);
        }
      },
      async cancel(reason) {
        release();
        try {
          await reader.cancel(reason);
        } finally {
          abort.abort(reason);
        }
      },
    });
    return new Response(body, {
      status: upstream.status,
      headers: responseHeaders,
    });
  } catch {
    release();
    return unavailable(req.headers.get("content-type"));
  }
}

// A stream's client reads an error only from the message that ends the stream, never from a failed response's body.
function unavailable(contentType: string | null): Response {
  const end = createEndStreamSerialization({}).serialize({
    metadata: new Headers(),
    error: new ConnectError("the Castor API is unreachable", Code.Unavailable),
  });
  return new Response(encodeEnvelope(endStreamFlag, end), {
    headers: {
      "content-type": contentType ?? contentTypeStreamProto,
      "cache-control": "no-store",
    },
  });
}
