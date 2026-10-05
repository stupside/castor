import assert from "node:assert/strict";
import test from "node:test";
import { watchUpdates } from "../src/features/casts/watch-updates.ts";

const status = { update: { case: "status", value: { state: { case: "casting" } } } };
const completed = { update: { case: "ended", value: { result: { case: "completed", value: {} } } } };

test("a successful stream close without Ended reports a lost watch", async () => {
  const received = [];
  async function* upstream() { yield status; }
  await assert.rejects(async () => {
    for await (const message of watchUpdates(upstream(), new AbortController().signal)) received.push(message);
  }, { rawMessage: "The status stream closed before the cast ended." });
  assert.deepEqual(received, [status]);
});

test("Ended completes the view and releases the upstream iterator immediately", async () => {
  let released = false;
  async function* upstream() {
    try {
      yield status;
      yield completed;
      throw new Error("should not wait for another update after Ended");
    } finally { released = true; }
  }
  const received = [];
  for await (const message of watchUpdates(upstream(), new AbortController().signal)) received.push(message);
  assert.deepEqual(received, [status, completed]);
  assert.equal(released, true);
});

test("an aborted watch ignores late updates and does not report an EOF error", async () => {
  const abort = new AbortController();
  const received = [];
  let released = false;
  async function* upstream() {
    try {
      yield status;
      abort.abort();
      yield completed;
    } finally { released = true; }
  }
  for await (const message of watchUpdates(upstream(), abort.signal)) received.push(message);
  assert.deepEqual(received, [status]);
  assert.equal(released, true);
});
