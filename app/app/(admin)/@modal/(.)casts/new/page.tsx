import { Sheet } from "@/components/sheet";
import { CastForm } from "@/features/cast-form/cast-form";

// /casts/new opened from inside the app; a reload or a shared link shows the full page instead.
export default function NewCastSheet() {
  return (
    <Sheet
      title="New cast"
      description="Send a video page or a media link to a device."
    >
      <CastForm />
    </Sheet>
  );
}
