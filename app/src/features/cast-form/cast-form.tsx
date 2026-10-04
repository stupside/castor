"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { Scene } from "@/components/scene";
import { Bubble } from "@/components/bubble";
import { Button } from "@/components/button";
import type { Mood } from "@/components/castor-logo";
import { Tv } from "@/components/tv";
import { DeviceType, type Device } from "@/gen/castor/v1/device_pb";
import { submitAction } from "./actions";

const kinds = [["pages", "A video page"], ["stream", "A direct file"]] as const;
const deviceTypes: Record<DeviceType, string> = {
  [DeviceType.UNSPECIFIED]: "Screen",
  [DeviceType.DLNA]: "DLNA",
  [DeviceType.CHROMECAST]: "Chromecast",
  [DeviceType.ROKU]: "Roku",
};

export function CastForm({ devices, casts }: { devices: Device[]; casts: React.ReactNode }) {
  const [state, action, pending] = useActionState(submitAction, {});
  const [deviceId, setDeviceId] = useState(devices.length === 1 ? devices[0].id : undefined);
  const [kind, setKind] = useState<(typeof kinds)[number][0]>("pages");
  const [draft, setDraft] = useState("");
  const [sent, setSent] = useState<string>();
  const end = useRef<HTMLDivElement>(null);
  const device = devices.find((d) => d.id === deviceId);
  const link = draft.trim();
  const validUrl = /^https?:\/\/\S+$/i.test(link);
  const stage = !device ? 1 : !sent ? 2 : 3;

  useEffect(() => { end.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }); }, [stage, pending, state]);


  const mood: Mood = state.error && !pending && stage === 3 ? "oops" : pending ? "think" : stage === 3 ? "cheer" : stage === 2 && link ? (validUrl ? "cheer" : "think") : "happy";
  const send = () => { if (validUrl) setSent(link); };
  const paste = async () => {
    try { setDraft(await navigator.clipboard.readText()); } catch { /* clipboard blocked: pasting by hand still works */ }
  };

  return <Scene screen={pending ? "loading" : stage === 3 ? "playing" : "idle"} title={<>Send anything.<br /><span className="text-peach">Watch anywhere.</span></>} mood={mood}>
    <div className="flex max-w-md flex-col gap-3">
      <Bubble>{!devices.length ? "I can’t see any screens. Turn a TV on, then refresh." : devices.length === 1 ? `Hi! I found ${devices[0].name}. What are we watching?` : "Hi! Which screen shall we send something to?"}</Bubble>
      {devices.length > 0 && stage === 1 && <div className="flex animate-rise flex-wrap justify-end gap-2">
        {devices.map((d) => <Button variant="choice" type="button" key={d.id} onClick={() => setDeviceId(d.id)}><Tv className="size-4" />{d.name}<span className="text-[10px] uppercase opacity-70">{deviceTypes[d.type] ?? "Screen"}</span></Button>)}
      </div>}

      {device && <>
        {devices.length > 1 && <Bubble me>{device.name} <button type="button" onClick={() => { setDeviceId(undefined); setSent(undefined); }} className="ml-2 text-xs font-bold underline opacity-80">change</button></Bubble>}
        {devices.length > 1 && <Bubble>Nice choice! What are we watching?</Bubble>}
      </>}

      {stage === 2 && <div className="animate-rise space-y-3">
        <div role="radiogroup" aria-label="Kind of link" className="flex flex-wrap justify-end gap-2">
          {kinds.map(([k, label]) => <button type="button" role="radio" aria-checked={kind === k} key={k} onClick={() => setKind(k)} className={`rounded-full border-2 px-3.5 py-1 text-xs font-extrabold transition ${kind === k ? "border-ember bg-ember text-white" : "border-white/25 text-white/70 hover:border-ember hover:text-ember"}`}>{label}</button>)}
        </div>
        <div className={`flex items-center gap-2 rounded-full border-2 bg-black/35 py-1.5 pl-5 pr-1.5 backdrop-blur-md transition ${link && !validUrl ? "border-ember" : validUrl ? "border-reed" : "border-white/20 focus-within:border-ember"}`}>
          <input autoFocus value={draft} placeholder="Paste a link: https://" aria-invalid={!!link && !validUrl} onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter") send(); }} className="min-w-0 flex-1 bg-transparent py-1.5 text-[15px] font-semibold text-[#fffaf2] outline-none focus-visible:outline-none placeholder:font-medium placeholder:text-muted/60" />
          {!draft && <button type="button" onClick={paste} className="rounded-full bg-white/10 px-3 py-1.5 text-xs font-extrabold text-cream hover:bg-white/20">Paste</button>}
          <Button variant="send" type="button" onClick={send} disabled={!validUrl} aria-label="Send"><span aria-hidden>↑</span></Button>
        </div>
        {link && !validUrl && <p className="pl-4 text-xs font-bold text-ember">Almost: it needs to start with https://</p>}
      </div>}

      {sent && <>
        <Bubble me><span className="block max-w-[16rem] truncate">{sent}</span><button type="button" onClick={() => setSent(undefined)} className="text-xs font-bold underline opacity-80">edit</button></Bubble>
        <Bubble>Mmm, tasty link! Ready when you are.</Bubble>
      </>}

      {stage === 3 && !pending && <form action={action} className="animate-rise">
        <input type="hidden" name="deviceId" value={deviceId} /><input type="hidden" name="kind" value={kind} /><input type="hidden" name="url" value={sent} />
        <Button variant="wide" name="intent" value="cast">{state.error ? "Try again" : "Start casting"}</Button>
      </form>}

      {pending && <Bubble>Gnawing a path to {device?.name}<span className="ml-1 inline-flex gap-0.5" aria-hidden>{[0, 1, 2].map((i) => <span key={i} className="animate-swim" style={{ animationDelay: `${i * 150}ms` }}>.</span>)}</span></Bubble>}
      {state.error && !pending && stage === 3 && <Bubble>Oops, I dropped it: {state.error}</Bubble>}
      <div ref={end} />
    </div>
    {casts}
  </Scene>;
}
