import { CastList } from "@/features/casts/cast-list";
import { CastForm } from "@/features/cast-form/cast-form";
import { listDevices } from "@/features/devices/queries";

export const dynamic = "force-dynamic";

export default async function Home() {
  return <CastForm devices={await listDevices()} casts={<CastList />} />;
}
