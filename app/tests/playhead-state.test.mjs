import assert from "node:assert/strict";
import test from "node:test";
import {
  elapsedAfter,
  livePosition,
} from "../src/features/casts/playhead-state.ts";

test("the clock keeps the time played since the last report across a pause", () => {
  let elapsed = { from: 40_000, ms: 0 };
  elapsed = elapsedAfter(elapsed, 40_000, 3_000);
  assert.equal(
    livePosition(elapsed, 40_000),
    43_000,
    "paused, the clock went back",
  );
  elapsed = elapsedAfter(elapsed, 40_000, 250);
  assert.equal(
    livePosition(elapsed, 40_000),
    43_250,
    "resumed, the clock started over",
  );
  assert.equal(livePosition(elapsedAfter(elapsed, 50_000, 0), 50_000), 50_000);
});
