"use client";

import { createClient } from "@connectrpc/connect";
import Link from "next/link";
import { useEffect, useState } from "react";
import { Bubble } from "@/components/bubble";
import { Button, buttonClass } from "@/components/button";
import { Scene } from "@/components/scene";
import type { Mood } from "@/components/castor-logo";
import { CastService, type CastStatus, type Ended } from "@/gen/castor/v1/cast_pb";
import { RecoveryAction } from "@/gen/castor/v1/revision_pb";
import { LogLevel, type LogLine } from "@/gen/castor/v1/log_pb";
import { browserTransport } from "@/lib/browser";
import { stopAction } from "./actions";
import { Diary } from "./diary";
import { journey } from "./phases";

const client = createClient(CastService, browserTransport);
const recoveryActions: Record<RecoveryAction, string> = {
  [RecoveryAction.UNSPECIFIED]: "unspecified",
  [RecoveryAction.SWITCH_CANDIDATE]: "switch candidate",
  [RecoveryAction.DECODE_AXIS]: "decode axis",
  [RecoveryAction.RELAX_READ]: "relax read",
  [RecoveryAction.SERVE_INSTEAD]: "serve instead",
};

export function WatchView({ castId }: { castId: string }) {
  const [status, setStatus] = useState<CastStatus>();
  const [lines, setLines] = useState<LogLine[]>([]);
  const [ended, setEnded] = useState<Ended>();
  const [error, setError] = useState<string>();
  const [run, setRun] = useState(0);

  useEffect(() => {
    const abort = new AbortController();
    (async () => {
      setError(undefined);
      try {
        for await (const { update } of client.watch({ castId, logs: LogLevel.INFO }, { signal: abort.signal })) {
          if (update.case === "status") setStatus(update.value);
          else if (update.case === "line") setLines((l) => [...l.slice(-199), update.value]);
          else if (update.case === "ended") setEnded(update.value);
        }
      } catch (e) {
        if (!abort.signal.aborted) setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => abort.abort();
  }, [castId, run]);

  const state = status?.state;
  const progress = state?.case === "measuring" || state?.case === "casting" ? state.value : undefined;
  const casting = state?.case === "casting" ? state.value : undefined;
  const result = ended?.result;
  const at = Math.max(0, journey.findIndex((j) => j.state === state?.case));
  const failed = result?.case === "failed";
  const lost = !!error && !ended;
  const [mood, say]: [Mood, string] =
    ended ? result?.case === "failed" ? ["oops", result.value.message || "Something went wrong."]
      : result?.case === "stopped" ? ["sleep", "Stopped. Home for a nap."]
      : ["cheer", "All done. Hope you enjoyed the show!"]
    : lost ? ["oops", "I lost the thread. Reconnect?"]
    : [at === journey.length - 1 ? "cheer" : "think", status ? journey[at].say : "Waking up the beaver…"];
  const over = !!ended;

  const title = lost ? "Lost the thread." : over ? failed ? "That didn’t work." : result?.case === "stopped" ? "Stopped." : "All done." : at === journey.length - 1 ? "Now playing." : "Getting ready…";

  return <Scene screen={over || lost ? "idle" : at === journey.length - 1 ? "playing" : "loading"} title={title} mood={mood}>
    <ol className="mb-5 flex max-w-md flex-col gap-2.5">
      {journey.map((j, i) => {
        const done = i < at || (over && !failed);
        const here = i === at && !over;
        const bad = failed && i === at;
        return <li key={j.label} className={`flex items-center gap-3 text-sm font-bold transition-colors ${done ? "text-reed" : here ? "text-white" : bad ? "text-red-300" : "text-white/35"}`}>
          <span className={`grid size-7 place-items-center rounded-full text-xs transition-colors duration-500 ${done ? "bg-reed text-ink" : here ? "animate-pulse bg-ember text-white" : bad ? "bg-red-400 text-ink" : "bg-white/15"}`}>{done ? "✓" : bad ? "!" : i + 1}</span>
          {j.label}
        </li>;
      })}
    </ol>
    <div className="flex max-w-md flex-col gap-3">
      <Bubble>{say}</Bubble>
      {progress && !over && (progress.streams > 0 || casting?.revision) && <p className="text-xs font-semibold text-white/60">
        {progress.streams > 0 && `${progress.streams} streams found${progress.castable === undefined ? "" : `, ${progress.castable} playable`}`}{casting && casting.attempt > 1 && ` · try ${casting.attempt}`}
        {casting?.revision && <span className="block">Changed plan: {recoveryActions[casting.revision.action] ?? "unspecified"}, {casting.revision.why}</span>}
      </p>}
      <div className="mt-1 flex flex-wrap items-center gap-3">
        {!over && !lost && <form action={stopAction.bind(null, castId)}><Button variant="glass">Stop</Button></form>}
        {lost && <Button variant="ember" onClick={() => setRun((n) => n + 1)}>Reconnect</Button>}
        {(over || lost) && <Link href="/" className={buttonClass("ember")}>Cast something else</Link>}
        <Link href="/" className={buttonClass("ghost")}>Home</Link>
      </div>
      {lines.length > 0 && <Diary lines={lines} open={failed} />}
    </div>
  </Scene>;
}
