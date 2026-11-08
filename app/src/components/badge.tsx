import * as stylex from "@stylexjs/stylex";
import { color, radius, space, text } from "./tokens.stylex";

export type Tone = keyof typeof tones;

// A live badge pulses its dot: what it labels is still moving.
export function Badge({
  tone = "neutral",
  live,
  children,
  sx,
  ...props
}: {
  tone?: Tone;
  live?: boolean;
} & React.ComponentProps<"span">) {
  return (
    <span {...props} sx={[styles.badge, tones[tone], sx]}>
      <span aria-hidden sx={[styles.dot, live && styles.live]} />
      {children}
    </span>
  );
}

const ping = stylex.keyframes({
  "0%": { boxShadow: "0 0 0 0 currentColor" },
  "100%": { boxShadow: "0 0 0 5px transparent" },
});

const styles = stylex.create({
  badge: {
    display: "inline-flex",
    alignItems: "center",
    gap: space.xs,
    height: 22,
    paddingInline: space.sm,
    borderRadius: radius.pill,
    fontSize: text.xs,
    fontWeight: 600,
    lineHeight: 1,
    whiteSpace: "nowrap",
  },
  dot: {
    width: 6,
    height: 6,
    borderRadius: radius.pill,
    backgroundColor: "currentColor",
  },
  live: {
    animationName: ping,
    animationDuration: "1.4s",
    animationTimingFunction: "ease-out",
    animationIterationCount: "infinite",
  },
});

const tones = stylex.create({
  neutral: { backgroundColor: color.sunken, color: color.inkSoft },
  accent: { backgroundColor: color.accentSoft, color: color.accentStrong },
  success: { backgroundColor: color.successSoft, color: color.successStrong },
  warning: { backgroundColor: color.warningSoft, color: color.warningStrong },
  danger: { backgroundColor: color.dangerSoft, color: color.dangerStrong },
});
