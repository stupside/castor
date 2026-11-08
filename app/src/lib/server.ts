import "server-only";
import { context, propagation } from "@opentelemetry/api";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { CastService } from "@/gen/castor/v1/cast_pb";
import { authorize } from "./authorize";
import { env } from "./env";

const api = createConnectTransport({
  baseUrl: env.api.url,
  httpVersion: "1.1",
  interceptors: [
    (next) => async (req) => {
      await authorize();
      if (env.api.token)
        req.header.set("Authorization", `Bearer ${env.api.token}`);
      propagation.inject(context.active(), req.header, {
        set: (headers, key, value) => headers.set(key, value),
      });
      return next(req);
    },
  ],
});

export const casts = createClient(CastService, api);
