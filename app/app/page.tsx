import { Suspense } from "react";
import type { Device } from "@/gen/castor/v1/device_pb";
import { CastList } from "@/features/casts/cast-list";
import { CastForm } from "@/features/cast-form/cast-form";
import { listDevices } from "@/features/devices/queries";
import { describe } from "@/lib/errors";

export const dynamic = "force-dynamic";

export default async function Home() {
  let devices: Device[] = [];
  let devicesError: string | undefined;
  try {
    devices = await listDevices();
  } catch (e) {
    devicesError = describe(e);
  }
  return <CastForm devices={devices} devicesError={devicesError} casts={<Suspense fallback={<p className="mt-10 text-sm text-peach" role="status">Checking active casts…</p>}><CastList /></Suspense>} />;
}
