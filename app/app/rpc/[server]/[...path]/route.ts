import { env } from "@/lib/env";

// Watch is the only call a browser makes; everything else runs in server actions.
const allowed = {
  api: { upstream: env.api, path: "castor.v1.CastService/Watch" },
  media: { upstream: env.media, path: "castor.media.v1.CastService/Watch" },
} as const;

export async function POST(req: Request, ctx: RouteContext<"/rpc/[server]/[...path]">) {
  const { server, path } = await ctx.params;
  const rule = allowed[server as keyof typeof allowed];
  if (!rule || path.join("/") !== rule.path) return new Response("not found", { status: 404 });

  const headers = new Headers(req.headers);
  for (const h of ["host", "cookie", "connection", "content-length"]) headers.delete(h);
  if (rule.upstream.token) headers.set("Authorization", `Bearer ${rule.upstream.token}`);

  const upstream = await fetch(`${rule.upstream.url}/${rule.path}`, {
    method: "POST",
    headers,
    body: req.body,
    duplex: "half",
    signal: req.signal,
    cache: "no-store",
  } as RequestInit);
  return new Response(upstream.body, { status: upstream.status, headers: upstream.headers });
}
