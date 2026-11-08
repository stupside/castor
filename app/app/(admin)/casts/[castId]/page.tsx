import type { Metadata } from "next";
import { deviceName, deviceTypes, sourceLabel } from "@/features/casts/labels";
import { WatchView } from "@/features/casts/watch-view";
import { CastSchema } from "@/gen/castor/v1/cast_pb";
import { casts } from "@/lib/server";
import { valid } from "@/lib/valid";

export const metadata: Metadata = { title: "Cast" };

export default async function CastWatch({
  params,
}: PageProps<"/casts/[castId]">) {
  const { castId } = await params;
  // Only ListCasts names the device and source; an ended cast is no longer listed.
  const cast = await casts
    .listCasts({})
    .then(({ casts }) => {
      const found = casts.find((c) => c.id === castId);
      return found && valid(CastSchema, found);
    })
    .catch(() => undefined);
  const device =
    cast?.device &&
    `${deviceName(cast.device)} · ${deviceTypes[cast.device.type]}`;

  return (
    <WatchView
      key={castId}
      castId={castId}
      device={device}
      source={cast && sourceLabel(cast.source)}
    />
  );
}
