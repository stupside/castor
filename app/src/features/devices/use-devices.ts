import { createClient } from "@connectrpc/connect";
import { startTransition, useEffect, useState } from "react";
import {
  DeviceService,
  WatchDevicesResponseSchema,
  type DeviceValid,
} from "@/gen/castor/v1/device_pb";
import { browserTransport } from "@/lib/browser";
import { describe } from "@/lib/errors";
import { valid } from "@/lib/valid";

const client = createClient(DeviceService, browserTransport);
const retryMs = 3000;

// devices stays undefined until the server first answers; error holds while it reconnects.
export type DeviceStream = { devices?: DeviceValid[]; error?: string };

// One stream per page however many lists show it, stopped once none does.
let latest: DeviceStream = {};
const listeners = new Set<() => void>();
let following: AbortController | undefined;

function publish(next: DeviceStream) {
  latest = next;
  for (const listener of listeners) listener();
}

// The server sends the whole list now and again on every change; a dropped stream reconnects.
async function follow(signal: AbortSignal) {
  while (!signal.aborted) {
    try {
      for await (const sent of client.watchDevices({}, { signal })) {
        publish({ devices: valid(WatchDevicesResponseSchema, sent).devices });
      }
    } catch (e) {
      if (signal.aborted) return;
      publish({ devices: latest.devices, error: describe(e) });
    }
    await new Promise((r) => setTimeout(r, retryMs));
  }
}

export function useDevices(): DeviceStream {
  const [stream, setStream] = useState(latest);
  useEffect(() => {
    // A transition lets rows that arrive or leave animate.
    const listener = () => startTransition(() => setStream(latest));
    listeners.add(listener);
    if (!following) {
      following = new AbortController();
      void follow(following.signal);
    }
    listener();
    return () => {
      listeners.delete(listener);
      // A list mounting next, as on a page change, keeps the stream and what it found.
      setTimeout(() => {
        if (listeners.size) return;
        following?.abort();
        following = undefined;
        latest = {};
      }, retryMs);
    };
  }, []);
  return stream;
}
