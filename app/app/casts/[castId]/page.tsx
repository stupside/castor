import { WatchView } from "@/features/casts/watch-view";

export default async function CastWatch({ params }: PageProps<"/casts/[castId]">) {
  const { castId } = await params;
  return <WatchView castId={castId} />;
}
