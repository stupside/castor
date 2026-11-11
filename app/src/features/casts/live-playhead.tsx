"use client";

import * as stylex from "@stylexjs/stylex";
import { Meter } from "@/components/meter";
import { color, space, text } from "@/components/tokens.stylex";
import { useEffect, useState } from "react";
import { clock } from "./labels";
import { type Elapsed, elapsedAfter, livePosition } from "./playhead-state";

// Between the device's reports, a playing cast moves on with the clock.
export function useLivePosition(
  position: number,
  paused: boolean,
  duration?: number,
) {
  const [elapsed, setElapsed] = useState<Elapsed>({ from: position, ms: 0 });
  useEffect(() => {
    if (paused) return;
    let last = performance.now();
    const id = setInterval(() => {
      const now = performance.now();
      setElapsed((e) => elapsedAfter(e, position, now - last));
      last = now;
    }, 250);
    return () => clearInterval(id);
  }, [position, paused]);
  const live = livePosition(elapsed, position);
  return duration ? Math.min(live, duration) : live;
}

export function LivePlayhead({
  position,
  duration,
  paused,
}: {
  position: number;
  duration?: number;
  paused: boolean;
}) {
  const live = useLivePosition(position, paused, duration);
  return (
    <div sx={styles.row}>
      {duration && (
        <Meter value={live} max={duration} label="Position" paused={paused} />
      )}
      <span sx={styles.time}>
        {clock(live)}
        {duration && ` / ${clock(duration)}`}
      </span>
    </div>
  );
}

const styles = stylex.create({
  row: {
    display: "flex",
    alignItems: "center",
    gap: space.md,
    marginTop: space.xs,
    maxWidth: 420,
  },
  time: {
    flexShrink: 0,
    fontSize: text.xs,
    fontVariantNumeric: "tabular-nums",
    color: color.inkMuted,
  },
});
