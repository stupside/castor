import { DeviceService } from "@/gen/castor/v1/device_pb";
import { accessDenied } from "@/lib/access";
import { env } from "@/lib/env";
import { proxyWatch } from "@/lib/watch-proxy";

export async function POST(req: Request) {
  return (
    accessDenied(req.headers) ??
    proxyWatch(DeviceService.method.watchDevices, req, env.api)
  );
}
