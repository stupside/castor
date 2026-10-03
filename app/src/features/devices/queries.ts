import "server-only";
import { unstable_cache } from "next/cache";
import { devices } from "@/lib/server";

export const listDevices = unstable_cache(
  async () => (await devices.listDevices({})).devices,
  ["castor-devices"],
  { revalidate: 15, tags: ["devices"] },
);
