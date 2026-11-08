import * as stylex from "@stylexjs/stylex";
import { Icon } from "./icon";
import { color, radius, space, text } from "./tokens.stylex";

// Danger interrupts (role alert); info waits to be read (role status).
export function Notice({
  tone = "info",
  children,
  sx,
  ...props
}: { tone?: keyof typeof tones } & React.ComponentProps<"div">) {
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      {...props}
      sx={[styles.notice, tones[tone], sx]}
    >
      <Icon name="alert" sx={styles.icon} />
      <div sx={styles.body}>{children}</div>
    </div>
  );
}

const styles = stylex.create({
  notice: {
    display: "flex",
    alignItems: "flex-start",
    gap: space.sm,
    paddingBlock: space.md,
    paddingInline: space.lg,
    borderRadius: radius.md,
    fontSize: text.md,
    lineHeight: 1.45,
  },
  icon: { flexShrink: 0 },
  body: {
    display: "grid",
    gap: space.xs,
    minWidth: 0,
    overflowWrap: "anywhere",
  },
});

const tones = stylex.create({
  info: { backgroundColor: color.accentSoft, color: color.accentStrong },
  danger: { backgroundColor: color.dangerSoft, color: color.dangerStrong },
});
