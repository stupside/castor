"use server";

import { durationFromMs } from "@bufbuild/protobuf/wkt";
import { revalidatePath } from "next/cache";
import { describe } from "@/lib/errors";
import { casts } from "@/lib/server";

// The cast's Watch stream reports the outcome; these return only a refusal.
async function control(call: () => Promise<unknown>) {
  try {
    await call();
  } catch (e) {
    return describe(e);
  }
}

export async function stopAction(castId: string) {
  const error = await control(() => casts.stop({ castId }));
  if (!error) revalidatePath("/");
  return error;
}

export async function pauseAction(castId: string) {
  return control(() => casts.pause({ castId }));
}

export async function resumeAction(castId: string) {
  return control(() => casts.resume({ castId }));
}

export async function seekAction(castId: string, ms: number) {
  return control(() => casts.seek({ castId, position: durationFromMs(ms) }));
}
