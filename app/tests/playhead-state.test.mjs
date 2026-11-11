import assert from "node:assert/strict";
import test from "node:test";
import {
  elapsedAfter,
  livePosition,
  seekShown,
} from "../src/features/casts/playhead-state.ts";

test("a seek holds over reports of the stream it leaves until its answer", () => {
  const asked = { request: 1, to: 600_000, from: 40_000, answered: false };
  const before = seekShown(asked, 45_000);
  assert.equal(
    before?.to,
    600_000,
    "a report before the answer cleared the seek",
  );
  const answered = { ...before, answered: true };
  assert.equal(seekShown(answered, 45_000), answered);
  assert.equal(seekShown(answered, 601_000), undefined);
});

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
