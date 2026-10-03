"use server";

import { revalidatePath } from "next/cache";
import { casts } from "@/lib/server";

export async function stopAction(castId: string) {
  await casts.stop({ castId });
  revalidatePath("/");
}
