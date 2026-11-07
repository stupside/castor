"use client";

import { useState } from "react";
import { DeviceType } from "@/gen/castor/v1/device_pb";
import { Button } from "@/components/button";

export type PinnedTarget = { type: DeviceType; address: string };

export function PinnedTargetPicker({ onSelect, onCancel }: { onSelect: (target: PinnedTarget) => void; onCancel: () => void }) {
  const [type, setType] = useState(DeviceType.DLNA);
  const [address, setAddress] = useState("");

  return <form className="animate-rise space-y-3 rounded-2xl bg-white/10 p-4" onSubmit={(event) => {
    event.preventDefault();
    if (address.trim()) onSelect({ type, address: address.trim() });
  }}>
    <p className="text-sm font-semibold text-cream">Enter the screen’s address on your network.</p>
    <div className="flex flex-wrap gap-2">
      <label className="sr-only" htmlFor="pinned-type">Screen type</label>
      <select id="pinned-type" value={type} onChange={(event) => setType(Number(event.target.value))} className="rounded-full bg-ink px-4 py-2 text-sm text-cream">
        <option value={DeviceType.DLNA}>DLNA</option>
        <option value={DeviceType.CHROMECAST}>Chromecast</option>
        <option value={DeviceType.ROKU}>Roku</option>
        <option value={DeviceType.FCAST}>FCast</option>
      </select>
      <input type="text" value={address} onChange={(event) => setAddress(event.target.value)} required autoCapitalize="none" spellCheck={false} aria-label="Screen address" placeholder="192.168.1.20" className="min-w-0 flex-1 rounded-full bg-ink px-4 py-2 text-sm text-cream placeholder:text-muted/60" />
      <Button variant="choice" type="submit">Use screen</Button>
      <Button variant="ghost" type="button" onClick={onCancel}>Cancel</Button>
    </div>
    <p className="text-xs text-white/60">Use an IP address or host name{type === DeviceType.DLNA ? ", or the DLNA description URL" : ""}.</p>
  </form>;
}
