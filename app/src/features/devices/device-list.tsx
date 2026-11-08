"use client";

import * as stylex from "@stylexjs/stylex";
import Link from "next/link";
import { Badge } from "@/components/badge";
import { ButtonLink } from "@/components/button";
import { Card } from "@/components/card";
import { CopyButton } from "@/components/copy-button";
import { EmptyState } from "@/components/empty-state";
import { Icon } from "@/components/icon";
import { List, ListItem, ListSkeleton } from "@/components/list";
import { Notice } from "@/components/notice";
import { Mono } from "@/components/text";
import { color, space, text } from "@/components/tokens.stylex";
import type { Device } from "@/gen/castor/v1/device_pb";
import { deviceName, deviceTypes } from "@/features/casts/labels";
import { useDevices, type DeviceStream } from "./use-devices";

// Compact is the dashboard's side panel; the full list is the Devices page.
export function DeviceList({ compact }: { compact?: boolean }) {
  const { devices, error } = useDevices();
  return (
    <Card
      flush
      title={compact ? "Devices" : "Found on the network"}
      description={devices ? `${devices.length} answering` : "Looking…"}
      actions={<LiveBadge devices={devices} error={error} />}
    >
      {error && <DevicesOffline error={error} sx={styles.notice} />}
      {!devices ? (
        <ListSkeleton />
      ) : devices.length ? (
        <List>
          {devices.map((device) => (
            <DeviceRow key={device.id} device={device} compact={compact} />
          ))}
        </List>
      ) : (
        <EmptyState
          title="No device answered"
          action={<ButtonLink href="/casts/new">Cast to an address</ButtonLink>}
        >
          Turn a device on and it shows up here. A device discovery can’t see
          still takes a cast by its address.
        </EmptyState>
      )}
      {compact && !!devices?.length && (
        <Link href="/devices" {...stylex.props(styles.more)}>
          All devices
          <Icon name="forward" size={14} />
        </Link>
      )}
    </Card>
  );
}

export function DevicesOffline({
  error,
  ...props
}: { error: string } & React.ComponentProps<typeof Notice>) {
  return (
    <Notice tone="danger" {...props}>
      Can’t reach the devices: {error}. Reconnecting…
    </Notice>
  );
}

export function LiveBadge({ devices, error }: DeviceStream) {
  if (error) return <Badge tone="danger">Offline</Badge>;
  if (!devices) return <Badge tone="accent">Connecting</Badge>;
  return (
    <Badge tone="success" live>
      Live
    </Badge>
  );
}

function DeviceRow({ device, compact }: { device: Device; compact?: boolean }) {
  const name = deviceName(device);
  return (
    <ListItem
      leading={<Icon name="device" />}
      title={
        <>
          {name}
          <Badge>{deviceTypes[device.type]}</Badge>
        </>
      }
      meta={
        <>
          <Mono>{device.address}</Mono>
          {!compact && (
            <CopyButton
              value={device.address}
              label={`Copy the address of ${name}`}
            />
          )}
        </>
      }
      trailing={
        <ButtonLink
          href={`/casts/new?device=${encodeURIComponent(device.id)}`}
          aria-label={compact ? `Cast to ${name}` : undefined}
          size={compact ? "icon" : "sm"}
          icon="cast"
        >
          {!compact && "Cast"}
        </ButtonLink>
      }
    />
  );
}

const styles = stylex.create({
  notice: { marginInline: space.xl, marginTop: space.lg },
  more: {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    gap: space.xs,
    paddingBlock: space.md,
    borderTopWidth: 1,
    borderTopStyle: "solid",
    borderTopColor: color.line,
    fontSize: text.sm,
    fontWeight: 600,
    color: { default: color.accentStrong, ":hover": color.ink },
    textDecoration: "none",
    backgroundColor: { default: "transparent", ":hover": color.canvas },
  },
});
