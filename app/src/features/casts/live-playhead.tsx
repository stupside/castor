"use client";

import * as stylex from "@stylexjs/stylex";
import { Meter } from "@/components/meter";
import { color, space, text } from "@/components/tokens.stylex";
import { useEffect, useState } from "react";
import { clock } from "./labels";

// Between the device's reports, a playing cast moves on with the clock.
export function useLivePosition(
  position: number,
  paused: boolean,
  duration?: number,
) {
  const [elapsed, setElapsed] = useState({ from: position, ms: 0 });
  useEffect(() => {
    if (paused) return;
    const start = performance.now();
    const id = setInterval(
      () => setElapsed({ from: position, ms: performance.now() - start }),
      250,
    );
    return () => clearInterval(id);
  }, [position, paused]);
  const live =
    !paused && elapsed.from === position ? position + elapsed.ms : position;
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
