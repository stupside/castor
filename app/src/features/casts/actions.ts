"use server";

import { revalidatePath } from "next/cache";
import { describe } from "@/lib/errors";
import { casts } from "@/lib/server";

export type StopState = { error?: string; stopped?: boolean };

export async function stopAction(castId: string, _: StopState): Promise<StopState> {
  try {
    await casts.stop({ castId });
  } catch (e) {
    return { error: describe(e) };
  }
  revalidatePath("/");
  return { stopped: true };
}
