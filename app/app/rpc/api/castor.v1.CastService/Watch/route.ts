import { CastService } from "@/gen/castor/v1/cast_pb";
import { accessDenied } from "@/lib/access";
import { env } from "@/lib/env";
import { proxyWatch } from "@/lib/watch-proxy";

export async function POST(req: Request) {
  return (
    accessDenied(req.headers) ??
    proxyWatch(CastService.method.watch, req, env.api)
  );
}
