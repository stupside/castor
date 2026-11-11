// The device's last report and the time played since it, on the browser's clock.
export type Elapsed = { from: number; ms: number };

// elapsedAfter adds delta of play to what was played since position was reported; a new report starts over.
export function elapsedAfter(
  elapsed: Elapsed,
  position: number,
  delta: number,
): Elapsed {
  return {
    from: position,
    ms: (elapsed.from === position ? elapsed.ms : 0) + delta,
  };
}

// livePosition is where the device plays now: its last report and the time played since, paused or not.
export function livePosition(elapsed: Elapsed, position: number): number {
  return elapsed.from === position ? position + elapsed.ms : position;
}
