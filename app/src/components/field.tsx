"use client";

import * as stylex from "@stylexjs/stylex";
import { createContext, use, useId } from "react";
import { color, motion, radius, shadow, space, text } from "./tokens.stylex";
import { typography } from "./text";

// The control inside a Field spreads these: its id, and the hint describing it.
export const FieldContext = createContext<{
  id?: string;
  "aria-describedby"?: string;
}>({});

export function Field({
  label,
  hint,
  children,
  sx,
  ...props
}: {
  label: React.ReactNode;
  hint?: React.ReactNode;
} & React.ComponentProps<"div">) {
  const id = useId();
  const hintId = hint ? `${id}-hint` : undefined;
  return (
    <div {...props} sx={[styles.field, sx]}>
      <label htmlFor={id} sx={styles.label}>
        {label}
      </label>
      <FieldContext value={{ id, "aria-describedby": hintId }}>
        {children}
      </FieldContext>
      {hint && (
        <span id={hintId} sx={styles.hint}>
          {hint}
        </span>
      )}
    </div>
  );
}

export function Input({ sx, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      {...use(FieldContext)}
      {...props}
      sx={[control.box, props.type === "url" && typography.mono, sx]}
    />
  );
}

export function Switch({
  label,
  sx,
  ...props
}: { label: React.ReactNode } & Omit<React.ComponentProps<"input">, "type">) {
  return (
    <label sx={[styles.switchRow, sx]}>
      <span sx={styles.switchTrack}>
        <input
          type="checkbox"
          role="switch"
          {...props}
          sx={[styles.cover, stylex.defaultMarker()]}
        />
        <span sx={styles.knob} />
      </span>
      {label}
    </label>
  );
}

// A radio drawn as a card, a whole row to pick; the radio inside it drives the look.
export function Choice({
  title,
  meta,
  leading,
  sx,
  ...props
}: {
  title: React.ReactNode;
  meta: React.ReactNode;
  leading: React.ReactNode;
} & Omit<React.ComponentProps<"input">, "type" | "title">) {
  return (
    <label sx={[styles.choice, sx]}>
      <input
        type="radio"
        {...props}
        sx={[styles.radio, stylex.defaultMarker()]}
      />
      <span sx={styles.choiceLeading}>{leading}</span>
      <span sx={styles.choiceText}>
        <span sx={styles.choiceTitle}>{title}</span>
        <span sx={styles.hint}>{meta}</span>
      </span>
    </label>
  );
}

// Radios in a pill; the gopher-blue thumb slides to the chosen one.
export function Segmented<T extends string>({
  name,
  label,
  options,
  value,
  onChange,
  sx,
  ...props
}: {
  name: string;
  label: string;
  options: readonly { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
} & Omit<React.ComponentProps<"div">, "onChange">) {
  const index = Math.max(
    0,
    options.findIndex((o) => o.value === value),
  );
  return (
    <div
      role="radiogroup"
      aria-label={label}
      {...props}
      sx={[styles.segmented, sx]}
    >
      <span
        aria-hidden
        sx={[styles.segmentThumb, styles.slot(index, options.length)]}
      />
      {options.map((option) => (
        <label key={option.value} sx={styles.segment}>
          <input
            type="radio"
            name={name}
            value={option.value}
            checked={option.value === value}
            onChange={() => onChange(option.value)}
            sx={[styles.cover, stylex.defaultMarker()]}
          />
          {option.label}
        </label>
      ))}
    </div>
  );
}

export const control = stylex.create({
  box: {
    width: "100%",
    minWidth: 0,
    height: 40,
    paddingInline: space.md,
    backgroundColor: color.surface,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: {
      default: color.lineStrong,
      ":hover": color.accent,
      ":focus-visible": color.accentStrong,
    },
    borderRadius: radius.md,
    fontSize: text.md,
    color: color.ink,
    outlineStyle: "none",
    boxShadow: { default: null, ":focus-visible": shadow.ring },
    transitionProperty: "border-color, box-shadow",
    transitionDuration: motion.fast,
    "::placeholder": { color: color.inkMuted },
  },
});

const styles = stylex.create({
  field: { display: "grid", gap: space.xs, minWidth: 0 },
  label: { fontSize: text.sm, fontWeight: 600, color: color.ink },
  hint: { fontSize: text.xs, lineHeight: 1.4, color: color.inkMuted },
  cover: {
    position: "absolute",
    inset: 0,
    width: "100%",
    height: "100%",
    margin: 0,
    opacity: 0,
    cursor: "pointer",
  },

  switchRow: {
    display: "flex",
    alignItems: "center",
    gap: space.md,
    fontSize: text.md,
    color: color.ink,
  },
  switchTrack: {
    position: "relative",
    flexShrink: 0,
    width: 42,
    height: 24,
    backgroundColor: {
      default: color.sunken,
      [stylex.when.descendant(":checked")]: color.gopher,
    },
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: {
      default: color.lineStrong,
      [stylex.when.descendant(":checked")]: color.ink,
    },
    borderRadius: radius.pill,
    boxShadow: {
      default: null,
      [stylex.when.descendant(":focus-visible")]: shadow.ring,
    },
    transitionProperty: "background-color, border-color",
    transitionDuration: motion.base,
  },
  knob: {
    position: "absolute",
    top: 2.5,
    left: 2.5,
    width: 16,
    height: 16,
    backgroundColor: color.surface,
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: color.ink,
    borderRadius: radius.pill,
    pointerEvents: "none",
    transform: {
      default: "none",
      [stylex.when.siblingBefore(":checked")]: "translateX(18px)",
    },
    transitionProperty: "transform",
    transitionDuration: motion.base,
    transitionTimingFunction: motion.spring,
  },

  choice: {
    display: "flex",
    alignItems: "center",
    gap: space.md,
    paddingBlock: space.md,
    paddingInline: space.lg,
    backgroundColor: {
      default: color.surface,
      ":hover": color.canvas,
      [stylex.when.descendant(":checked")]: color.accentSoft,
    },
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: {
      default: color.line,
      ":hover": color.lineStrong,
      [stylex.when.descendant(":checked")]: color.ink,
    },
    borderRadius: radius.md,
    boxShadow: {
      default: null,
      [stylex.when.descendant(":checked")]: shadow.inked,
      [stylex.when.descendant(":focus-visible")]: shadow.ring,
    },
    transform: {
      default: null,
      [stylex.when.descendant(":checked")]: "translateY(-1px)",
    },
    transitionProperty: "background-color, border-color, box-shadow, transform",
    transitionDuration: motion.base,
    transitionTimingFunction: motion.spring,
  },
  radio: {
    appearance: "none",
    flexShrink: 0,
    width: 18,
    height: 18,
    margin: 0,
    backgroundColor: { default: color.surface, ":checked": color.ink },
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: { default: color.lineStrong, ":checked": color.ink },
    borderRadius: radius.pill,
    boxShadow: {
      default: `inset 0 0 0 8px ${color.surface}`,
      ":checked": `inset 0 0 0 3.5px ${color.surface}`,
    },
    outlineStyle: "none",
    cursor: "pointer",
    transitionProperty: "box-shadow, border-color",
    transitionDuration: motion.base,
    transitionTimingFunction: motion.spring,
  },
  choiceLeading: { display: "grid", color: color.inkSoft },
  choiceText: { display: "grid", gap: space.xxs, minWidth: 0 },
  choiceTitle: {
    fontSize: text.md,
    fontWeight: 600,
    color: color.ink,
    overflowWrap: "anywhere",
  },

  segmented: {
    position: "relative",
    isolation: "isolate",
    display: "grid",
    gridAutoColumns: "1fr",
    gridAutoFlow: "column",
    padding: 3,
    backgroundColor: color.sunken,
    borderWidth: 1,
    borderStyle: "solid",
    borderColor: color.line,
    borderRadius: radius.pill,
  },
  segmentThumb: {
    position: "absolute",
    zIndex: -1,
    insetBlock: 3,
    left: 3,
    backgroundColor: color.gopher,
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: color.ink,
    borderRadius: radius.pill,
    boxShadow: shadow.inked,
    transitionProperty: "transform",
    transitionDuration: motion.slow,
    transitionTimingFunction: motion.spring,
  },
  slot: (index: number, count: number) => ({
    width: `calc((100% - 6px) / ${count})`,
    transform: `translateX(${index * 100}%)`,
  }),
  segment: {
    position: "relative",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    height: 36,
    paddingInline: space.lg,
    borderRadius: radius.pill,
    fontSize: text.md,
    fontWeight: 600,
    color: {
      default: color.inkMuted,
      ":hover": color.ink,
      [stylex.when.descendant(":checked")]: color.ink,
    },
    boxShadow: {
      default: null,
      [stylex.when.descendant(":focus-visible")]: shadow.ring,
    },
    userSelect: "none",
    transitionProperty: "color",
    transitionDuration: motion.base,
  },
});
