import type { CastStatus } from "@/gen/castor/v1/cast_pb";

// The journey, in the order a cast moves through it.
export const journey = [
  { state: "connecting", label: "Connect", say: "Knocking on the screen's door…" },
  { state: "extracting", label: "Find", say: "Sniffing out the video…" },
  { state: "measuring", label: "Measure", say: "Sizing up the best stream…" },
  { state: "casting", label: "Cast", say: "Floating it downriver. Enjoy!" },
] as const satisfies readonly { state: NonNullable<CastStatus["state"]["case"]>; label: string; say: string }[];
