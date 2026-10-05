import Link from "next/link";
import type { Cast } from "@/gen/castor/v1/cast_pb";
import { describe } from "@/lib/errors";
import { casts } from "@/lib/server";
import { StopButton } from "./stop-button";
import { AutoRefresh } from "./auto-refresh";
import { journey } from "./phases";

export async function CastList() {
  let activeCasts: Cast[];
  try {
    ({ casts: activeCasts } = await casts.listCasts({}));
  } catch (e) {
    return <section className="mt-10 max-w-md">
      <AutoRefresh />
      <p role="status" className="text-sm font-semibold text-peach">Can’t check active casts: {describe(e)}. Retrying…</p>
    </section>;
  }
  return (
    <section className="mt-10 max-w-md space-y-3">
      <AutoRefresh />
      {activeCasts.length > 0 && <h2 className="text-xs font-extrabold uppercase tracking-[0.18em] text-peach">Active casts</h2>}
      <ul className="grid gap-3">
        {activeCasts.map((c) => {
          const step = journey.find((j) => j.state === c.status?.state.case) ?? journey[0];
          return (
            <li key={c.id} className="animate-rise flex items-center gap-3 rounded-2xl bg-white/10 p-4 text-[#fffaf2]">
              <Link href={`/casts/${encodeURIComponent(c.id)}`} className="group flex min-w-0 flex-1 items-center gap-3">
                <span className="relative grid size-3 place-items-center"><span className="absolute size-3 animate-ping-soft rounded-full bg-ember" /><span className="size-3 rounded-full bg-ember" /></span>
                <span className="min-w-0"><span className="block truncate font-semibold group-hover:underline">{c.device?.name || c.device?.address}</span><span className="block text-xs text-white/60">{step.say}</span></span>
              </Link>
              <StopButton castId={c.id} />
            </li>
          );
        })}
      </ul>
    </section>
  );
}
