"use client";

import { useActionState, useEffect, useRef, useState } from "react";
import { Scene } from "@/components/scene";
import { Bubble } from "@/components/bubble";
import { Button } from "@/components/button";
import type { Mood } from "@/components/castor-logo";
import { Tv } from "@/components/tv";
import { DeviceType, type Device } from "@/gen/castor/v1/device_pb";
import { RefreshDevices } from "@/features/devices/refresh-devices";
import { submitAction } from "./actions";

const kinds = [["pages", "A video page"], ["stream", "A direct file"]] as const;
const deviceTypes: Record<DeviceType, string> = {
  [DeviceType.UNSPECIFIED]: "Screen",
  [DeviceType.DLNA]: "DLNA",
  [DeviceType.CHROMECAST]: "Chromecast",
  [DeviceType.ROKU]: "Roku",
};

export function CastForm({ devices, devicesError, casts }: { devices: Device[]; devicesError?: string; casts: React.ReactNode }) {
  const [state, action, pending] = useActionState(submitAction, {});
  const [deviceId, setDeviceId] = useState<string | null | undefined>(devices.length === 1 ? devices[0].id : undefined);
  const [kind, setKind] = useState<(typeof kinds)[number][0]>("pages");
  const [draft, setDraft] = useState("");
  const [sent, setSent] = useState<string>();
  const end = useRef<HTMLDivElement>(null);
  const device = devices.find((d) => d.id === deviceId);
  if (deviceId === undefined && devices.length === 1) setDeviceId(devices[0].id);
  if (deviceId && !device) {
    setDeviceId(null);
    setSent(undefined);
  }
  const link = draft.trim();
  let validUrl = false;
  try {
    const url = new URL(link);
    validUrl = /^https?:\/\//i.test(link) && (url.protocol === "https:" || url.protocol === "http:") && !!url.hostname && !/\s/.test(link);
  } catch { /* Keep the editor available until the URL is complete. */ }
  const stage = !device ? 1 : !sent ? 2 : 3;
  const error = state.deviceId === deviceId && state.kind === kind && state.url === sent ? state.error : undefined;

  useEffect(() => {
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    end.current?.scrollIntoView({ behavior: reducedMotion ? "instant" : "smooth", block: "nearest" });
  }, [stage, pending, state]);


  const mood: Mood = error && !pending && stage === 3 ? "oops" : pending ? "think" : stage === 3 ? "cheer" : stage === 2 && link ? (validUrl ? "cheer" : "think") : "happy";
  const send = () => { if (validUrl) setSent(link.replace(/^https?:/i, (scheme) => scheme.toLowerCase())); };
  const paste = async () => {
    try { setDraft(await navigator.clipboard.readText()); } catch { /* clipboard blocked: pasting by hand still works */ }
  };

  return <Scene screen={pending ? "loading" : "idle"} title={<>Send anything.<br /><span className="text-peach">Watch anywhere.</span></>} mood={mood}>
    <div className="flex max-w-md flex-col gap-3">
      <Bubble>{devicesError ? `I couldn’t check the screens: ${devicesError}. Try refreshing.` : !devices.length ? "I can’t see any screens. Turn a TV on, then refresh." : devices.length === 1 && device ? `Hi! I found ${device.name}. What are we watching?` : "Hi! Which screen shall we send something to?"}</Bubble>
      <RefreshDevices disabled={pending} />
      {devices.length > 0 && stage === 1 && <div className="flex animate-rise flex-wrap justify-end gap-2">
        {devices.map((d) => <Button variant="choice" type="button" key={d.id} onClick={() => setDeviceId(d.id)}><Tv className="size-4" />{d.name}<span className="text-[10px] uppercase opacity-70">{deviceTypes[d.type] ?? "Screen"}</span></Button>)}
      </div>}

      {device && <>
        {devices.length > 1 && <Bubble me>{device.name} <button type="button" disabled={pending} onClick={() => { setDeviceId(null); setSent(undefined); }} className="ml-2 text-xs font-bold underline opacity-80 disabled:opacity-40">change</button></Bubble>}
        {devices.length > 1 && <Bubble>Nice choice! What are we watching?</Bubble>}
      </>}

      {stage === 2 && <form onSubmit={(e) => { e.preventDefault(); send(); }} className="animate-rise space-y-3">
        <div role="radiogroup" aria-label="Kind of link" className="flex flex-wrap justify-end gap-2">
          {kinds.map(([k, label]) => <button type="button" role="radio" aria-checked={kind === k} key={k} onClick={() => setKind(k)} className={`rounded-full border-2 px-3.5 py-1 text-xs font-extrabold transition ${kind === k ? "border-ember bg-ember text-white" : "border-white/25 text-white/70 hover:border-ember hover:text-ember"}`}>{label}</button>)}
        </div>
        <div className={`flex items-center gap-2 rounded-full border-2 bg-black/35 py-1.5 pl-5 pr-1.5 backdrop-blur-md transition ${link && !validUrl ? "border-ember" : validUrl ? "border-reed" : "border-white/20 focus-within:border-ember"}`}>
          <input type="url" required autoFocus value={draft} aria-label="Video link" autoCapitalize="none" spellCheck={false} placeholder="Paste a link: https://" aria-invalid={!!link && !validUrl} onChange={(e) => setDraft(e.target.value)}
            className="min-w-0 flex-1 bg-transparent py-1.5 text-[15px] font-semibold text-[#fffaf2] outline-none focus-visible:outline-none placeholder:font-medium placeholder:text-muted/60" />
          {!draft && <button type="button" onClick={paste} className="rounded-full bg-white/10 px-3 py-1.5 text-xs font-extrabold text-cream hover:bg-white/20">Paste</button>}
          <Button variant="send" type="submit" disabled={!validUrl} aria-label="Send"><span aria-hidden>↑</span></Button>
        </div>
        {link && !validUrl && <p role="status" className="pl-4 text-xs font-bold text-ember">Use a complete http:// or https:// link, without spaces.</p>}
      </form>}

      {sent && <>
        <Bubble me><span className="block max-w-[16rem] truncate">{sent}</span><button type="button" disabled={pending} onClick={() => setSent(undefined)} className="text-xs font-bold underline opacity-80 disabled:opacity-40">edit</button></Bubble>
        <Bubble>Mmm, tasty link! Ready when you are.</Bubble>
      </>}

      {stage === 3 && <form action={action} className="animate-rise" aria-busy={pending}>
        <input type="hidden" name="deviceId" value={device?.id} /><input type="hidden" name="kind" value={kind} /><input type="hidden" name="url" value={sent} />
        <Button variant="wide" disabled={pending}>{pending ? "Starting…" : error ? "Try again" : "Start casting"}</Button>
      </form>}

      {pending && <Bubble>Gnawing a path to {device?.name}<span className="ml-1 inline-flex gap-0.5" aria-hidden>{[0, 1, 2].map((i) => <span key={i} className="animate-swim" style={{ animationDelay: `${i * 150}ms` }}>.</span>)}</span></Bubble>}
      {error && !pending && stage === 3 && <div role="alert"><Bubble>Oops, I dropped it: {error}</Bubble></div>}
      <div ref={end} />
    </div>
    {casts}
  </Scene>;
}
