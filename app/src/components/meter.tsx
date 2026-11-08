import * as stylex from "@stylexjs/stylex";
import { color, motion, radius } from "./tokens.stylex";

// A thin read-only bar; Slider is the one to drag.
export function Meter({
  value,
  max,
  label,
  paused,
  sx,
  ...props
}: {
  value: number;
  max: number;
  label: string;
  paused?: boolean;
} & React.ComponentProps<"span">) {
  const percent = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <span
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={value}
      {...props}
      sx={[styles.track, sx]}
    >
      <span
        sx={[styles.fill, paused && styles.paused, styles.width(percent)]}
      />
    </span>
  );
}

const styles = stylex.create({
  track: {
    position: "relative",
    display: "block",
    width: "100%",
    height: 4,
    overflow: "hidden",
    backgroundColor: color.sunken,
    borderRadius: radius.pill,
  },
  fill: {
    position: "absolute",
    insetBlock: 0,
    left: 0,
    backgroundColor: color.accent,
    borderRadius: radius.pill,
    transitionProperty: "width, background-color",
    transitionDuration: motion.slow,
    transitionTimingFunction: "linear",
  },
  paused: { backgroundColor: color.warning },
  width: (percent: number) => ({ width: `${percent}%` }),
});
