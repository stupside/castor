import * as stylex from "@stylexjs/stylex";
import { Icon } from "./icon";
import { color, media, motion, radius, space, text } from "./tokens.stylex";

export type StepState = keyof typeof marks;

// On wide screens a line joins the steps and fills as each one is done.
export function Steps({
  steps,
  sx,
  ...props
}: {
  steps: readonly { label: string; state: StepState }[];
} & React.ComponentProps<"ol">) {
  return (
    <ol {...props} sx={[styles.steps, sx]}>
      {steps.map((step, i) => (
        <li
          key={step.label}
          aria-current={step.state === "current" ? "step" : undefined}
          sx={[styles.step, labels[step.state]]}
        >
          <span sx={[styles.mark, marks[step.state]]}>
            {step.state === "done" ? (
              <Icon name="check" size={14} />
            ) : step.state === "failed" ? (
              "!"
            ) : (
              i + 1
            )}
          </span>
          {step.label}
          {i < steps.length - 1 && (
            <span aria-hidden sx={styles.line}>
              <span
                sx={[styles.lineFill, step.state === "done" && styles.filled]}
              />
            </span>
          )}
        </li>
      ))}
    </ol>
  );
}

const pulse = stylex.keyframes({
  "0%": {
    boxShadow: `0 0 0 0 color-mix(in srgb, ${color.accent} 45%, transparent)`,
  },
  "100%": { boxShadow: "0 0 0 8px transparent" },
});

const styles = stylex.create({
  steps: {
    display: { default: "grid", [media.wide]: "flex" },
    gridTemplateColumns: "1fr 1fr",
    gap: space.md,
    padding: 0,
    listStyle: "none",
  },
  step: {
    display: "flex",
    flexGrow: 1,
    alignItems: "center",
    gap: space.sm,
    fontSize: text.md,
    fontWeight: 600,
    whiteSpace: "nowrap",
    transitionProperty: "color",
    transitionDuration: motion.base,
  },
  mark: {
    display: "grid",
    placeItems: "center",
    flexShrink: 0,
    width: 28,
    height: 28,
    borderWidth: 1.5,
    borderStyle: "solid",
    borderRadius: radius.pill,
    fontSize: text.xs,
    fontWeight: 700,
    transitionProperty: "background-color, border-color, color, transform",
    transitionDuration: motion.slow,
    transitionTimingFunction: motion.spring,
  },
  line: {
    display: { default: "none", [media.wide]: "block" },
    flexGrow: 1,
    height: 2,
    minWidth: space.lg,
    marginInline: space.sm,
    overflow: "hidden",
    backgroundColor: color.line,
    borderRadius: radius.pill,
  },
  lineFill: {
    display: "block",
    height: "100%",
    backgroundColor: color.success,
    transform: "scaleX(0)",
    transformOrigin: "left",
    transitionProperty: "transform",
    transitionDuration: "600ms",
    transitionTimingFunction: motion.out,
  },
  filled: { transform: "scaleX(1)" },
});

const labels = stylex.create({
  done: { color: color.ink },
  current: { color: color.ink },
  failed: { color: color.dangerStrong },
  todo: { color: color.inkMuted },
});

const marks = stylex.create({
  done: {
    color: color.ink,
    backgroundColor: color.success,
    borderColor: color.ink,
  },
  current: {
    color: color.ink,
    backgroundColor: color.gopher,
    borderColor: color.ink,
    transform: "scale(1.08)",
    animationName: pulse,
    animationDuration: "1.6s",
    animationIterationCount: "infinite",
  },
  failed: {
    color: color.dangerStrong,
    backgroundColor: color.dangerSoft,
    borderColor: color.danger,
  },
  todo: {
    color: color.inkMuted,
    backgroundColor: color.surface,
    borderColor: color.lineStrong,
  },
});
