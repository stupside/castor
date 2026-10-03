import { Phase } from "@/gen/castor/v1/cast_pb";

// The journey, in the order a cast moves through it.
export const journey = [
  { phase: Phase.CONNECTING, label: "Connect", say: "Knocking on the screen's door…" },
  { phase: Phase.EXTRACTING, label: "Find", say: "Sniffing out the video…" },
  { phase: Phase.MEASURING, label: "Measure", say: "Sizing up the best stream…" },
  { phase: Phase.CASTING, label: "Cast", say: "Floating it downriver. Enjoy!" },
] as const;
