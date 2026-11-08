"use client";

import * as stylex from "@stylexjs/stylex";
import { useSearchParams } from "next/navigation";
import { useActionState, useState } from "react";
import { Button } from "@/components/button";
import { Card } from "@/components/card";
import { Choice, Field, Input, Segmented, Switch } from "@/components/field";
import { Icon } from "@/components/icon";
import { Row, Stack } from "@/components/layout";
import { Notice } from "@/components/notice";
import { Skeleton } from "@/components/skeleton";
import { Select } from "@/components/select";
import { media, space } from "@/components/tokens.stylex";
import type { Source, SubtitleSelection } from "@/gen/castor/v1/cast_pb";
import { DeviceType, DeviceTypeSchema } from "@/gen/castor/v1/device_pb";
import { Delivery, DeliverySchema } from "@/gen/castor/v1/stream_pb";
import { deviceName, deviceTypes } from "@/features/casts/labels";
import { DevicesOffline, LiveBadge } from "@/features/devices/device-list";
import { useDevices } from "@/features/devices/use-devices";
import { submitAction } from "./actions";

const kinds = [
  {
    value: "pages",
    label: "Video page",
    field: "Page URL",
    name: "source.pages.urls",
    hint: "Castor finds the video on the page.",
  },
  {
    value: "stream",
    label: "Media URL",
    field: "Media URL",
    name: "source.stream.url",
    hint: "A direct link to the media, played as it is.",
  },
] as const satisfies readonly {
  value: NonNullable<Source["source"]["case"]>;
  label: string;
  field: string;
  name: string;
  hint: string;
}[];

const pinnedTypes = Object.values(DeviceType)
  .filter(
    (type): type is DeviceType =>
      typeof type === "number" && type !== DeviceType.UNSPECIFIED,
  )
  .map((type) => ({
    value: DeviceTypeSchema.value[type].name,
    label: deviceTypes[type],
  }));

const deliveries = [
  { value: "", label: "Server default" },
  { value: DeliverySchema.value[Delivery.AUTO].name, label: "Automatic" },
  {
    value: DeliverySchema.value[Delivery.SERVE].name,
    label: "Relay through castor",
  },
];

const heights = [
  { value: "", label: "Server default" },
  { value: "480", label: "480p" },
  { value: "720", label: "720p" },
  { value: "1080", label: "1080p" },
  { value: "2160", label: "2160p (4K)" },
];

const subtitleModes = [
  { value: "", label: "Server default" },
  { value: "disabled", label: "Off" },
  { value: "preferences", label: "Choose a language" },
] as const satisfies readonly {
  value: "" | NonNullable<SubtitleSelection["mode"]["case"]>;
  label: string;
}[];

const languageHint = "Two or three letters, such as en or fra.";

const languageInput = {
  maxLength: 3,
  autoCapitalize: "none",
  spellCheck: false,
  onInput: (e: React.FormEvent<HTMLInputElement>) => {
    e.currentTarget.value = e.currentTarget.value.toLowerCase();
  },
} as const;

// Inputs are named by their CastRequest path; the server alone validates (noValidate).
export function CastForm() {
  const [state, action, pending] = useActionState(submitAction, {});
  const values = state.values ?? {};
  const named = (name: string) => ({ name, defaultValue: values[name] });
  const { devices, error } = useDevices();
  // A device id, or "pinned" for an address discovery can't see.
  const [target, setTarget] = useState(useSearchParams().get("device") ?? "");
  if (target === "" && devices?.length === 1) setTarget(devices[0].id);
  const [kind, setKind] = useState<(typeof kinds)[number]["value"]>(
    kinds.find((k) => values[k.name])?.value ?? "pages",
  );
  const link = kinds.find((k) => k.value === kind) ?? kinds[0];
  const [subtitles, setSubtitles] = useState(
    values["preferences.subtitles"] ?? "",
  );
  const pinned = target === "pinned";

  return (
    <form action={action} noValidate aria-busy={pending} sx={styles.form}>
      <Card
        title="Device"
        description="Where the video plays."
        actions={<LiveBadge devices={devices} error={error} />}
      >
        <Stack>
          {error && <DevicesOffline error={error} />}
          <div role="radiogroup" aria-label="Device" sx={styles.choices}>
            {!devices && <Skeleton height="62px" />}
            {devices?.map((device) => (
              <Choice
                key={device.id}
                name="target.deviceId"
                value={device.id}
                checked={target === device.id}
                onChange={() => setTarget(device.id)}
                leading={<Icon name="device" />}
                title={deviceName(device)}
                meta={`${deviceTypes[device.type]} · ${device.address}`}
              />
            ))}
            <Choice
              name="target.deviceId"
              value=""
              checked={pinned}
              onChange={() => setTarget("pinned")}
              leading={<Icon name="plus" />}
              title="Another address"
              meta="A device discovery can’t see"
            />
          </div>
          {pinned && (
            <div sx={styles.pair}>
              <Field label="Type">
                <Select
                  options={pinnedTypes}
                  {...named("target.pinned.type")}
                />
              </Field>
              <Field
                label="Address"
                hint="An IP address or host name, or a DLNA description URL."
              >
                <Input
                  {...named("target.pinned.address")}
                  autoCapitalize="none"
                  spellCheck={false}
                  placeholder="192.168.1.20"
                />
              </Field>
            </div>
          )}
        </Stack>
      </Card>

      <Card title="Source" description="What to play.">
        <Stack>
          <Segmented
            name="kind"
            label="Kind of link"
            options={kinds}
            value={kind}
            onChange={setKind}
          />
          <Field label={link.field} hint={link.hint}>
            <Input
              {...named(link.name)}
              type="url"
              autoCapitalize="none"
              spellCheck={false}
              placeholder="https://"
            />
          </Field>
        </Stack>
      </Card>

      <Card
        title="Playback"
        description="A field left on Server default keeps the API server’s setting."
      >
        <div sx={styles.pair}>
          <Field
            label="Delivery"
            hint="Automatic passes compatible media through; a relay encodes, on a hardware encoder when one works."
          >
            <Select options={deliveries} {...named("preferences.delivery")} />
          </Field>
          <Field label="Maximum height">
            <Select options={heights} {...named("preferences.maxHeight")} />
          </Field>
          <Field label="Audio language" hint={languageHint}>
            <Input
              {...named("preferences.audio.languages")}
              {...languageInput}
              placeholder="Server default"
            />
          </Field>
          <Field label="Subtitles">
            <Select
              name="preferences.subtitles"
              options={subtitleModes}
              value={subtitles}
              onChange={(e) => setSubtitles(e.target.value)}
            />
          </Field>
          {subtitles === "preferences" && (
            <>
              <Field label="Subtitle language" hint={languageHint}>
                <Input
                  {...named("preferences.subtitles.preferences.languages")}
                  {...languageInput}
                  placeholder="en"
                />
              </Field>
              <Switch
                sx={styles.switch}
                name="preferences.subtitles.preferences.transcribeIfMissing"
                label="Generate subtitles if the source has none"
                defaultChecked={
                  values[
                    "preferences.subtitles.preferences.transcribeIfMissing"
                  ] === "on"
                }
              />
            </>
          )}
        </div>
      </Card>

      {state.error && <Notice tone="danger">{state.error}</Notice>}
      <Row>
        <Button variant="primary" icon="cast" loading={pending}>
          {pending ? "Starting…" : "Start cast"}
        </Button>
      </Row>
    </form>
  );
}

const styles = stylex.create({
  form: { display: "grid", gap: space.lg, maxWidth: 760 },
  choices: {
    display: "grid",
    gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))",
    gap: space.sm,
  },
  pair: {
    display: "grid",
    gridTemplateColumns: { default: "1fr", [media.wide]: "1fr 1fr" },
    alignItems: "start",
    gap: space.lg,
  },
  switch: { alignSelf: "end", minHeight: 40, display: "flex" },
});
