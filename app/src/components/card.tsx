import * as stylex from "@stylexjs/stylex";
import { color, radius, shadow, space, text } from "./tokens.stylex";

// A flush card lets a List run edge to edge under its header.
export function Card({
  title,
  description,
  actions,
  flush,
  children,
  sx,
  ...props
}: {
  title?: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  flush?: boolean;
} & Omit<React.ComponentProps<"section">, "title">) {
  return (
    <section {...props} sx={[styles.card, sx]}>
      {(title || actions) && (
        <header sx={styles.header}>
          <div sx={styles.heading}>
            {title && <h2 sx={styles.title}>{title}</h2>}
            {description && <p sx={styles.description}>{description}</p>}
          </div>
          {actions}
        </header>
      )}
      {flush ? children : <div sx={styles.body}>{children}</div>}
    </section>
  );
}

const styles = stylex.create({
  card: {
    overflow: "hidden",
    backgroundColor: color.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: color.line,
    borderRadius: radius.lg,
    boxShadow: shadow.card,
  },
  header: {
    display: "flex",
    flexWrap: "wrap",
    alignItems: "center",
    justifyContent: "space-between",
    gap: space.md,
    paddingBlock: space.lg,
    paddingInline: space.xl,
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: color.line,
  },
  heading: { display: "grid", gap: space.xxs, minWidth: 0 },
  title: { fontSize: text.lg, fontWeight: 600, color: color.ink },
  description: { fontSize: text.sm, color: color.inkMuted },
  body: { padding: space.xl },
});
