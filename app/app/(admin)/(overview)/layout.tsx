import * as stylex from "@stylexjs/stylex";
import { Page } from "@/components/page";
import { media, space } from "@/components/tokens.stylex";

// The casts and the @devices panel load, stream and fail on their own.
export default function Overview({
  children,
  devices,
}: {
  children: React.ReactNode;
  devices: React.ReactNode;
}) {
  return (
    <Page title="Casts" description="What’s playing on your devices, live.">
      <div sx={styles.grid}>
        <div sx={styles.main}>{children}</div>
        <aside aria-label="Devices">{devices}</aside>
      </div>
    </Page>
  );
}

const styles = stylex.create({
  grid: {
    display: "grid",
    gridTemplateColumns: {
      default: "minmax(0, 1fr)",
      [media.wide]: "minmax(0, 1fr) 300px",
    },
    alignItems: "start",
    gap: space.xl,
  },
  main: { minWidth: 0 },
});
