import type { CastStatus, Source } from "@/gen/castor/v1/cast_pb";
import { DeviceType, type Device } from "@/gen/castor/v1/device_pb";
import type { Tone } from "@/components/badge";

// The journey, in the order a cast moves through it.
export const journey = [
  { state: "connecting", label: "Connect", now: "Connecting" },
  { state: "extracting", label: "Find video", now: "Finding video" },
  { state: "measuring", label: "Measure", now: "Measuring" },
  { state: "casting", label: "Play", now: "Playing" },
] as const satisfies readonly {
  state: NonNullable<CastStatus["state"]["case"]>;
  label: string;
  now: string;
}[];

export const deviceTypes: Record<DeviceType, string> = {
  [DeviceType.UNSPECIFIED]: "Device",
  [DeviceType.DLNA]: "DLNA",
  [DeviceType.CHROMECAST]: "Chromecast",
  [DeviceType.ROKU]: "Roku",
};

// deviceName is what a device is called, its address where it has no name.
export const deviceName = (d: Device) => d.name || d.address;

export function stateBadge(status?: CastStatus): {
  label: string;
  tone: Tone;
  live: boolean;
} {
  const state = status?.state;
  if (state?.case === "casting")
    return state.value.playhead?.paused
      ? { label: "Paused", tone: "warning", live: false }
      : { label: "Playing", tone: "success", live: true };
  return {
    label: (journey.find((j) => j.state === state?.case) ?? journey[0]).now,
    tone: "accent",
    live: true,
  };
}

export function sourceLabel(source?: Source): string {
  const s = source?.source;
  if (s?.case === "stream") return s.value.url;
  if (s?.case === "pages")
    return s.value.urls.length > 1
      ? `${s.value.urls[0]} +${s.value.urls.length - 1}`
      : s.value.urls[0];
  if (s?.case === "streams")
    return `${s.value.streams.length} resolved streams`;
  return "";
}

export function clock(ms: number): string {
  const total = Math.floor(ms / 1000),
    h = Math.floor(total / 3600),
    m = Math.floor(total / 60) % 60,
    s = total % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}
