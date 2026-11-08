import type { Metadata } from "next";
import { Page } from "@/components/page";
import { CastForm } from "@/features/cast-form/cast-form";

export const metadata: Metadata = { title: "New cast" };

export default function NewCast() {
  return (
    <Page
      title="New cast"
      description="Send a video page or a media link to a device."
    >
      <CastForm />
    </Page>
  );
}
