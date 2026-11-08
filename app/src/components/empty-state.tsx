import * as stylex from "@stylexjs/stylex";
import { Gopher } from "./gopher";
import { color, radius, space, text } from "./tokens.stylex";

export function EmptyState({
  title,
  children,
  action,
  sx,
  ...props
}: {
  title: React.ReactNode;
  action: React.ReactNode;
} & Omit<React.ComponentProps<"div">, "title">) {
  return (
    <div {...props} sx={[styles.empty, sx]}>
      <span sx={[styles.art, stylex.defaultMarker()]}>
        <Gopher size={72} />
      </span>
      <h3 sx={styles.title}>{title}</h3>
      <p sx={styles.body}>{children}</p>
      {action}
    </div>
  );
}

const bob = stylex.keyframes({
  "0%, 100%": { transform: "translateY(0)" },
  "50%": { transform: "translateY(-4px)" },
});

const styles = stylex.create({
  empty: {
    display: "grid",
    justifyItems: "center",
    gap: space.sm,
    paddingBlock: space.xxxl,
    paddingInline: space.xl,
    textAlign: "center",
  },
  art: {
    display: "grid",
    placeItems: "center",
    width: 104,
    height: 104,
    marginBottom: space.sm,
    borderRadius: radius.pill,
    backgroundColor: color.cream,
    animationName: bob,
    animationDuration: "3.2s",
    animationTimingFunction: "ease-in-out",
    animationIterationCount: "infinite",
  },
  title: { fontSize: text.lg, fontWeight: 600, color: color.ink },
  body: {
    maxWidth: 380,
    marginBottom: space.sm,
    fontSize: text.md,
    lineHeight: 1.5,
    color: color.inkMuted,
  },
});
