"use client";

import { useEffect, useRef } from "react";
import { LogLevel, type LogLine } from "@/gen/castor/v1/log_pb";

export function Diary({ lines, open }: { lines: LogLine[]; open?: boolean }) {
  const list = useRef<HTMLOListElement>(null);
  useEffect(() => { list.current?.scrollTo({ top: list.current.scrollHeight }); }, [lines]);
  return <details open={open || undefined} className="mt-3 rounded-2xl bg-black/25 p-4 backdrop-blur-md">
    <summary className="text-xs font-extrabold uppercase tracking-[0.14em] text-peach">The beaver’s diary ({lines.length})</summary>
    <ol ref={list} className="mt-3 flex max-h-64 flex-col gap-2.5 overflow-y-auto pr-1">
      {lines.map((l, i) => <li key={i} className="flex gap-2.5 text-[13px] leading-5 text-[#fffaf2]/90">
        <span className={`mt-1.5 size-2 shrink-0 rounded-full ${l.level >= LogLevel.ERROR ? "bg-red-400" : l.level === LogLevel.WARN ? "bg-peach" : "bg-reed"}`} />
        <span className="min-w-0"><span className="font-semibold">{l.message.charAt(0).toUpperCase() + l.message.slice(1)}</span>
          {l.attrs.length > 0 && <span className="mt-1 flex flex-wrap gap-1">{l.attrs.map((a) => <span key={a.key} className="max-w-full truncate rounded-full bg-white/10 px-2 py-0.5 text-[11px] font-semibold text-white/60"><span className="text-white/40">{a.key}</span> {a.value}</span>)}</span>}
        </span>
      </li>)}
    </ol>
  </details>;
}
