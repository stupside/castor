import { Code, ConnectError } from "@connectrpc/connect";
import type { WatchResponse } from "@/gen/castor/v1/cast_pb";

// A successful transport close is not a completed cast: only Ended is terminal.
export async function* watchUpdates(updates: AsyncIterable<WatchResponse>, signal: AbortSignal) {
  for await (const message of updates) {
    if (signal.aborted) return;
    yield message;
    if (message.update.case === "ended") return;
  }
  if (!signal.aborted) throw new ConnectError("The status stream closed before the cast ended.", Code.Unavailable);
}
