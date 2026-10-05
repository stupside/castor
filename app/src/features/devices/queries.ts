import "server-only";
import { unstable_cache } from "next/cache";
import { devices } from "@/lib/server";
import { env } from "@/lib/env";

export const listDevices = unstable_cache(
  async () => (await devices.listDevices({})).devices,
  ["castor-devices", env.api.url],
  { revalidate: 15, tags: ["devices"] },
);
