import { env } from "@/lib/env";

// Watch is the only call a browser makes; everything else runs in server actions.
export async function POST(req: Request) {
  const headers = new Headers(req.headers);
  for (const h of ["host", "cookie", "authorization", "connection", "content-length"]) headers.delete(h);
  if (env.api.token) headers.set("Authorization", `Bearer ${env.api.token}`);

  const init: RequestInit & { duplex: "half" } = {
    method: "POST",
    headers,
    body: req.body,
    duplex: "half",
    signal: req.signal,
    cache: "no-store",
  };
  const upstream = await fetch(`${env.api.url}/castor.v1.CastService/Watch`, init);
  return new Response(upstream.body, { status: upstream.status, headers: upstream.headers });
}
