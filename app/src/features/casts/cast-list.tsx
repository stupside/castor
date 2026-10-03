import Link from "next/link";
import { Button } from "@/components/button";
import { stopAction } from "./actions";
import { AutoRefresh } from "./auto-refresh";
import { journey } from "./phases";
import { listCasts } from "./queries";

export async function CastList() {
  const casts = await listCasts();
  return (
    <section className="mt-10 max-w-md space-y-3">
      <AutoRefresh />
      {casts.length > 0 && <h2 className="text-xs font-extrabold uppercase tracking-[0.18em] text-peach">Now playing</h2>}
      <ul className="grid gap-3">
        {casts.map((c) => {
          const step = journey.find((j) => j.phase === c.status?.phase) ?? journey[0];
          return (
            <li key={c.id} className="animate-rise flex items-center gap-3 rounded-2xl bg-white/10 p-4 text-[#fffaf2]">
              <Link href={`/casts/${c.id}`} className="group flex min-w-0 flex-1 items-center gap-3">
                <span className="relative grid size-3 place-items-center"><span className="absolute size-3 animate-ping-soft rounded-full bg-ember" /><span className="size-3 rounded-full bg-ember" /></span>
                <span className="min-w-0"><span className="block truncate font-semibold group-hover:underline">{c.device?.name || c.device?.address}</span><span className="block text-xs text-white/60">{step.say}</span></span>
              </Link>
              <form action={stopAction.bind(null, c.id)}><Button variant="stop">Stop</Button></form>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
