"use server";

import { redirect } from "next/navigation";
import { describe } from "@/lib/errors";
import { casts } from "@/lib/server";
import { preferences, source } from "./request";

export type FormState = { error?: string };

export async function submitAction(_: FormState, f: FormData): Promise<FormState> {
  let castId: string;
  try {
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
