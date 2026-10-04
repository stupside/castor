import "server-only";
import type { MessageInitShape } from "@bufbuild/protobuf";
import type { PreferencesSchema, SourceSchema } from "@/gen/castor/v1/cast_pb";

const text = (f: FormData, k: string) => String(f.get(k) ?? "").trim();

export const source = (f: FormData): MessageInitShape<typeof SourceSchema> =>
  text(f, "kind") === "pages"
    ? { source: { case: "pages", value: { urls: text(f, "url").split(/\s+/).filter(Boolean) } } }
    : { source: { case: "stream", value: { url: text(f, "url") } } };

// Blank inherits the server's configuration; explicit subtitle choices set the mode.
export const preferences = (f: FormData): MessageInitShape<typeof PreferencesSchema> => {
  const delivery = text(f, "delivery");
  const height = text(f, "maxHeight");
  const subs = text(f, "subtitles");
  return {
    delivery: delivery ? Number(delivery) : undefined,
    maxHeight: height ? Number(height) : undefined,
    subtitles: !subs ? undefined : {
      mode: subs === "off" ? { case: "disabled", value: {} }
        : subs === "auto" ? { case: "autoDetect", value: {} }
        : { case: "language", value: subs },
    },
  };
};
