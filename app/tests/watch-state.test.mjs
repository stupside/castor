import assert from "node:assert/strict";
import test from "node:test";
import { watchState } from "../src/features/casts/watch-state.ts";

test("completion retains progress and cannot become a transport failure", () => {
  const status = { state: { case: "casting" } };
  const ended = { result: { case: "completed", value: {} } };
  const state = watchState(
    { phase: "watching", status },
    { type: "ended", ended },
  );
  assert.deepEqual(state, { phase: "ended", status, ended });
  assert.equal(
    watchState(state, { type: "lost", error: "late failure" }),
    state,
  );
  assert.equal(watchState(state, { type: "status", status: {} }), state);
});

test("reconnecting clears the previous outcome and accepts fresh status", () => {
  const lost = watchState(
    { phase: "watching" },
    { type: "lost", error: "offline" },
  );
  const watching = watchState(lost, { type: "restart" });
  assert.deepEqual(watching, { phase: "watching" });
  const status = { state: { case: "measuring" } };
  assert.deepEqual(watchState(watching, { type: "status", status }), {
    phase: "watching",
    status,
  });
});
