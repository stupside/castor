import { env } from "@/lib/env";
import { proxyWatch } from "@/lib/watch-proxy";

// Watch is the only call a browser makes; everything else runs in server actions.
export async function POST(req: Request) {
  return proxyWatch(req, env.api);
}
