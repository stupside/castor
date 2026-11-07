import type { MessageInitShape } from "@bufbuild/protobuf";
import { DeviceType, type TargetSchema } from "@/gen/castor/v1/device_pb";

const pinnedTypes: readonly DeviceType[] = [DeviceType.DLNA, DeviceType.CHROMECAST, DeviceType.ROKU, DeviceType.FCAST];

export function target(f: FormData): MessageInitShape<typeof TargetSchema> {
  if (f.get("targetKind") === "pinned") {
    const type = Number(f.get("deviceType")) as DeviceType;
    const address = String(f.get("address") ?? "").trim();
    if (!pinnedTypes.includes(type) || !address) throw new Error("Choose a screen type and enter its address.");
    return { target: { case: "pinned", value: { type, address } } };
  }

  const id = String(f.get("deviceId") ?? "").trim();
  if (!id) throw new Error("Choose a screen before casting.");
  return { target: { case: "deviceId", value: id } };
}
