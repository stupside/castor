import { durationMs } from "@bufbuild/protobuf/wkt";
import * as stylex from "@stylexjs/stylex";
import Link from "next/link";
import { Badge } from "@/components/badge";
import { ButtonLink } from "@/components/button";
import { Card } from "@/components/card";
import { EmptyState } from "@/components/empty-state";
import { Icon } from "@/components/icon";
import { List, ListItem, ListSkeleton } from "@/components/list";
import { Notice } from "@/components/notice";
import { Mono } from "@/components/text";
import { color } from "@/components/tokens.stylex";
import {
  ListCastsResponseSchema,
  type CastValid,
} from "@/gen/castor/v1/cast_pb";
import { describe } from "@/lib/errors";
import { casts } from "@/lib/server";
import { valid } from "@/lib/valid";
import { AutoRefresh } from "./auto-refresh";
import { deviceName, deviceTypes, sourceLabel, stateBadge } from "./labels";
import { LivePlayhead } from "./live-playhead";
import { StopButton } from "./stop-button";

export async function CastList() {
  let active: CastValid[];
  try {
    ({ casts: active } = valid(
      ListCastsResponseSchema,
      await casts.listCasts({}),
    ));
  } catch (e) {
    return (
      <>
        <AutoRefresh />
        <Notice tone="danger">
          Can’t check active casts: {describe(e)}. Retrying…
        </Notice>
      </>
    );
  }

  return (
    <Card
      flush
      title="Playing now"
      description={active.length === 1 ? "1 cast" : `${active.length} casts`}
    >
      <AutoRefresh />
      {active.length ? (
        <List>
          {active.map((cast) => (
            <CastRow key={cast.id} cast={cast} />
          ))}
        </List>
      ) : (
        <EmptyState
          title="Nothing is playing"
          action={
            <ButtonLink href="/casts/new" variant="primary" icon="plus">
              New cast
            </ButtonLink>
          }
        >
          Send a video page or a media link to a device and it shows up here.
        </EmptyState>
      )}
    </Card>
  );
}

export function CastListSkeleton() {
  return (
    <Card flush title="Playing now" description="Checking…">
      <ListSkeleton />
    </Card>
  );
}

function CastRow({ cast }: { cast: CastValid }) {
  const href = `/casts/${encodeURIComponent(cast.id)}` as const;
  const badge = stateBadge(cast.status);
  const state = cast.status?.state;
  const playhead = state?.case === "casting" ? state.value.playhead : undefined;

  return (
    <ListItem
      leading={<Icon name="device" />}
      title={
        <>
          <Link href={href} {...stylex.props(styles.link)}>
            {cast.device && deviceName(cast.device)}
          </Link>
          {cast.device && <Badge>{deviceTypes[cast.device.type]}</Badge>}
          <Badge tone={badge.tone} live={badge.live}>
            {badge.label}
          </Badge>
        </>
      }
      meta={<Mono>{sourceLabel(cast.source)}</Mono>}
      footer={
        playhead && (
          <LivePlayhead
            position={durationMs(playhead.position)}
            duration={playhead.duration && durationMs(playhead.duration)}
            paused={playhead.paused}
          />
        )
      }
      trailing={
        <>
          <ButtonLink href={href} size="sm">
            Open
          </ButtonLink>
          <StopButton castId={cast.id} size="sm" />
        </>
      }
    />
  );
}

const styles = stylex.create({
  link: {
    color: { default: color.ink, ":hover": color.accentStrong },
    textDecoration: { default: "none", ":hover": "underline" },
    textUnderlineOffset: 3,
  },
});
