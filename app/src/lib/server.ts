import "server-only";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { CastService } from "@/gen/castor/v1/cast_pb";
import { DeviceService } from "@/gen/castor/v1/device_pb";
import { env } from "./env";

const api = createConnectTransport({
  baseUrl: env.api.url,
  httpVersion: "1.1",
  interceptors: [(next) => (req) => {
    if (env.api.token) req.header.set("Authorization", `Bearer ${env.api.token}`);
    return next(req);
  }],
});

export const devices = createClient(DeviceService, api);
export const casts = createClient(CastService, api);
