import "server-only";
import { createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { CastService } from "@/gen/castor/v1/cast_pb";
import { DeviceService } from "@/gen/castor/v1/device_pb";
import { StreamService } from "@/gen/castor/media/v1/stream_pb";
import { env } from "./env";

const bearer = (token: string): Interceptor => (next) => (req) => {
  if (token) req.header.set("Authorization", `Bearer ${token}`);
  return next(req);
};

const transport = ({ url, token }: { url: string; token: string }) =>
  createConnectTransport({ baseUrl: url, httpVersion: "1.1", interceptors: [bearer(token)] });

const api = transport(env.api);

export const devices = createClient(DeviceService, api);
export const casts = createClient(CastService, api);
export const media = { stream: createClient(StreamService, transport(env.media)) };
