import type { Metadata } from "next";
import { Page } from "@/components/page";
import { DeviceList } from "@/features/devices/device-list";

export const metadata: Metadata = { title: "Devices" };

export default function Devices() {
  return (
    <Page
      title="Devices"
      description="The screens this server can reach on your network, live."
    >
      <DeviceList />
    </Page>
  );
}
