import * as stylex from "@stylexjs/stylex";
import { color, radius } from "./tokens.stylex";

export function Skeleton({
  width = "100%",
  height = "14px",
  sx,
  ...props
}: { width?: string; height?: string } & React.ComponentProps<"span">) {
  return (
    <span
      aria-hidden
      {...props}
      sx={[styles.skeleton, styles.size(width, height), sx]}
    />
  );
}

const shimmer = stylex.keyframes({
  from: { backgroundPosition: "150% 0" },
  to: { backgroundPosition: "-50% 0" },
});

const styles = stylex.create({
  skeleton: {
    display: "block",
    borderRadius: radius.sm,
    backgroundImage: `linear-gradient(90deg, ${color.sunken} 25%, ${color.line} 50%, ${color.sunken} 75%)`,
    backgroundSize: "200% 100%",
    animationName: shimmer,
    animationDuration: "1.4s",
    animationTimingFunction: "linear",
    animationIterationCount: "infinite",
  },
  size: (width: string, height: string) => ({ width, height }),
});
