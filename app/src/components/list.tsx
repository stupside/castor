import * as stylex from "@stylexjs/stylex";
import { ViewTransition } from "react";
import { Skeleton } from "./skeleton";
import { color, media, motion, radius, space, text } from "./tokens.stylex";

export function List({ sx, ...props }: React.ComponentProps<"ul">) {
  return <ul {...props} sx={[styles.list, sx]} />;
}

export function ListSkeleton() {
  return (
    <List>
      {[0, 1].map((i) => (
        <ListItem
          key={i}
          leading={<Skeleton width="20px" height="20px" />}
          title={<Skeleton width="140px" />}
          meta={<Skeleton width="200px" height="12px" />}
        />
      ))}
    </List>
  );
}

// A row that arrives (a new cast, a device answering) slides in; one that leaves folds away.
export function ListItem({
  leading,
  title,
  meta,
  trailing,
  footer,
  sx,
  ...props
}: {
  leading: React.ReactNode;
  title: React.ReactNode;
  meta: React.ReactNode;
  trailing?: React.ReactNode;
  footer?: React.ReactNode;
} & Omit<React.ComponentProps<"li">, "title">) {
  return (
    <ViewTransition enter={arrive} exit={leave} default="none">
      <li {...props} sx={[styles.item, stylex.defaultMarker(), sx]}>
        <span sx={styles.leading}>{leading}</span>
        <div sx={styles.text}>
          <div sx={styles.title}>{title}</div>
          <div sx={styles.meta}>{meta}</div>
          {footer}
        </div>
        {trailing && <div sx={styles.trailing}>{trailing}</div>}
      </li>
    </ViewTransition>
  );
}

const slideIn = stylex.keyframes({
  from: { opacity: 0, transform: "translateX(-16px)" },
  to: { opacity: 1, transform: "none" },
});

const foldAway = stylex.keyframes({
  to: { opacity: 0, transform: "scale(0.98)" },
});

const arrive = stylex.viewTransitionClass({
  new: {
    animationName: slideIn,
    animationDuration: motion.slow,
    animationTimingFunction: motion.spring,
  },
});

const leave = stylex.viewTransitionClass({
  old: {
    animationName: foldAway,
    animationDuration: motion.base,
    animationTimingFunction: "ease-in",
  },
});

const styles = stylex.create({
  list: { padding: 0, listStyle: "none" },
  item: {
    display: "flex",
    flexWrap: { default: "wrap", [media.wide]: "nowrap" },
    alignItems: "center",
    gap: space.md,
    paddingBlock: space.lg,
    paddingInline: space.xl,
    backgroundColor: { default: color.surface, ":hover": color.canvas },
    borderTopWidth: { default: 1, ":first-child": 0 },
    borderTopStyle: "solid",
    borderTopColor: color.line,
    transitionProperty: "background-color",
    transitionDuration: motion.fast,
  },
  leading: {
    display: "grid",
    placeItems: "center",
    flexShrink: 0,
    width: 40,
    height: 40,
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: {
      default: "transparent",
      [stylex.when.ancestor(":hover")]: color.ink,
    },
    borderRadius: radius.md,
    backgroundColor: {
      default: color.accentSoft,
      [stylex.when.ancestor(":hover")]: color.gopher,
    },
    color: {
      default: color.accentStrong,
      [stylex.when.ancestor(":hover")]: color.ink,
    },
    transform: {
      default: null,
      [stylex.when.ancestor(":hover")]: "rotate(-4deg)",
    },
    transitionProperty: "background-color, border-color, color, transform",
    transitionDuration: motion.base,
    transitionTimingFunction: motion.spring,
  },
  text: {
    display: "grid",
    flexGrow: 1,
    flexBasis: 0,
    gap: space.xxs,
    minWidth: 160,
  },
  title: {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    gap: space.sm,
    fontSize: text.md,
    fontWeight: 600,
    color: color.ink,
    overflowWrap: "anywhere",
  },
  meta: {
    display: "flex",
    alignItems: "center",
    gap: space.xs,
    fontSize: text.sm,
    color: color.inkMuted,
    overflowWrap: "anywhere",
  },
  trailing: {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    gap: space.sm,
    marginInlineStart: "auto",
  },
});
