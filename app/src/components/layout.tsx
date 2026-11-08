import * as stylex from "@stylexjs/stylex";
import { space } from "./tokens.stylex";

type Gap = keyof typeof gaps;

export function Stack({
  gap = "lg",
  sx,
  ...props
}: { gap?: Gap } & React.ComponentProps<"div">) {
  return <div {...props} sx={[styles.stack, gaps[gap], sx]} />;
}

export function Row({ sx, ...props }: React.ComponentProps<"div">) {
  return <div {...props} sx={[styles.row, sx]} />;
}

const styles = stylex.create({
  stack: { display: "grid", minWidth: 0 },
  row: {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    gap: space.sm,
  },
});

const gaps = stylex.create({
  sm: { gap: space.sm },
  lg: { gap: space.lg },
  xl: { gap: space.xl },
});
