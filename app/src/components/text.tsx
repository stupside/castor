import * as stylex from "@stylexjs/stylex";
import { color, font, radius, space, text } from "./tokens.stylex";

export function Mono({ sx, ...props }: React.ComponentProps<"code">) {
  return <code {...props} sx={[typography.mono, sx]} />;
}

export function Kbd({ sx, ...props }: React.ComponentProps<"kbd">) {
  return <kbd {...props} sx={[styles.kbd, sx]} />;
}

export const typography = stylex.create({
  mono: { fontFamily: font.mono, fontSize: text.sm },
});

const styles = stylex.create({
  kbd: {
    display: "inline-grid",
    placeItems: "center",
    minWidth: 22,
    height: 22,
    paddingInline: space.xs,
    backgroundColor: color.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: color.lineStrong,
    borderBottomWidth: 2,
    borderRadius: radius.sm,
    fontFamily: font.mono,
    fontSize: text.xs,
    color: color.inkSoft,
  },
});
