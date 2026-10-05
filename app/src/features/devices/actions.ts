"use server";

import { revalidatePath, updateTag } from "next/cache";

export async function refreshDevicesAction() {
  updateTag("devices");
  revalidatePath("/");
}
