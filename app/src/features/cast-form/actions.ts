"use server";

import { redirect } from "next/navigation";
import { describe } from "@/lib/errors";
import { casts } from "@/lib/server";
import { preferences, source } from "./request";

export type FormState = { error?: string; ranked?: { url: string; bitrate: bigint; lastResort: boolean }[] };

export async function submitAction(_: FormState, f: FormData): Promise<FormState> {
  let castId: string;
  try {
    if (f.get("intent") === "resolve") {
      const { ranked } = await casts.resolve({ source: source(f), preferences: preferences(f) });
      return { ranked };
    }
    ({ castId } = await casts.cast({
      target: { target: { case: "deviceId", value: String(f.get("deviceId") ?? "") } },
      source: source(f),
      preferences: preferences(f),
    }));
  } catch (e) {
    return { error: describe(e) };
  }
  redirect(`/casts/${castId}`);
}
