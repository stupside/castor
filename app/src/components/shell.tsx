import * as stylex from "@stylexjs/stylex";
import Link from "next/link";
import { Gopher } from "./gopher";
import { Nav } from "./nav";
import { color, media, motion, space, text } from "./tokens.stylex";

// A sidebar on wide screens, a top bar on phones.
export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div sx={styles.shell}>
      <aside sx={styles.side}>
        <Link href="/" {...stylex.props(styles.brand, stylex.defaultMarker())}>
          <Gopher size={40} />
          <span sx={styles.word}>castor</span>
        </Link>
        <Nav />
      </aside>
      <main sx={styles.main}>{children}</main>
    </div>
  );
}

const styles = stylex.create({
  shell: {
    display: { default: "block", [media.wide]: "grid" },
    gridTemplateColumns: "240px minmax(0, 1fr)",
    minHeight: "100svh",
  },
  side: {
    position: { default: "static", [media.wide]: "sticky" },
    top: 0,
    display: "flex",
    flexDirection: { default: "row", [media.wide]: "column" },
    flexWrap: { default: "wrap", [media.wide]: "nowrap" },
    alignItems: { default: "center", [media.wide]: "stretch" },
    gap: { default: space.md, [media.wide]: space.xl },
    height: { default: "auto", [media.wide]: "100svh" },
    paddingBlock: { default: space.md, [media.wide]: space.xl },
    paddingInline: space.lg,
    backgroundColor: color.surface,
    borderColor: color.line,
    borderBottomStyle: { default: "solid", [media.wide]: "none" },
    borderBottomWidth: 1,
    borderInlineEndStyle: { default: "none", [media.wide]: "solid" },
    borderInlineEndWidth: 1,
  },
  brand: {
    display: "flex",
    alignItems: "center",
    gap: space.sm,
    flexGrow: { default: 1, [media.wide]: 0 },
    paddingInline: { default: 0, [media.wide]: space.sm },
    color: color.ink,
    textDecoration: "none",
  },
  word: {
    fontSize: text.xl,
    fontWeight: 700,
    letterSpacing: "-0.04em",
    transform: {
      default: "none",
      [stylex.when.ancestor(":hover")]: "translateX(2px)",
    },
    transitionProperty: "transform",
    transitionDuration: motion.base,
    transitionTimingFunction: motion.spring,
  },
  main: {
    width: "100%",
    maxWidth: 1040,
    marginInline: "auto",
    paddingBlock: { default: space.xl, [media.wide]: space.xxxl },
    paddingInline: { default: space.lg, [media.wide]: space.xxxl },
  },
});
