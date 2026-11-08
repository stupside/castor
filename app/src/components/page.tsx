import * as stylex from "@stylexjs/stylex";
import { ViewTransition } from "react";
import { color, media, motion, space, text } from "./tokens.stylex";

// Pages slide in and fade out on navigation; a refresh of the same page doesn't animate.
export function Page({
  title,
  description,
  actions,
  children,
  sx,
  ...props
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
} & Omit<React.ComponentProps<"div">, "title">) {
  return (
    <ViewTransition enter={enter} exit={exit} default="none">
      <div {...props} sx={sx}>
        <header sx={styles.header}>
          <div sx={styles.heading}>
            <h1 sx={styles.title}>{title}</h1>
            {description && <div sx={styles.description}>{description}</div>}
          </div>
          {actions && <div sx={styles.actions}>{actions}</div>}
        </header>
        {children}
      </div>
    </ViewTransition>
  );
}

const rise = stylex.keyframes({
  from: { opacity: 0, transform: "translateY(12px)" },
  to: { opacity: 1, transform: "none" },
});

const fade = stylex.keyframes({ to: { opacity: 0 } });

const enter = stylex.viewTransitionClass({
  new: {
    animationName: rise,
    animationDuration: motion.slow,
    animationTimingFunction: motion.out,
  },
});

const exit = stylex.viewTransitionClass({
  old: {
    animationName: fade,
    animationDuration: motion.fast,
    animationTimingFunction: "ease-in",
  },
});

const styles = stylex.create({
  header: {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "flex-end",
    justifyContent: "space-between",
    gap: space.lg,
    marginBottom: { default: space.xl, [media.wide]: space.xxl },
  },
  heading: { display: "grid", gap: space.xs, minWidth: 0 },
  title: {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    gap: space.md,
    fontSize: text.xxl,
    fontWeight: 700,
    lineHeight: 1.15,
    letterSpacing: "-0.02em",
    color: color.ink,
    overflowWrap: "anywhere",
  },
  description: {
    display: "flex",
    alignItems: "center",
    gap: space.xs,
    fontSize: text.md,
    color: color.inkMuted,
    overflowWrap: "anywhere",
  },
  actions: { display: "flex", flexWrap: "wrap", gap: space.sm },
});
