"use client";

import * as stylex from "@stylexjs/stylex";
import { use } from "react";
import { control, FieldContext } from "./field";
import { color, motion, radius, shadow, space, text } from "./tokens.stylex";

// A native select; where the browser supports base-select, its picker is drawn in the theme.
export function Select({
  options,
  sx,
  ...props
}: {
  options: readonly { value: string; label: string }[];
} & React.ComponentProps<"select">) {
  return (
    <select
      {...use(FieldContext)}
      {...props}
      sx={[control.box, styles.select, sx]}
    >
      {options.map((option) => (
        <option key={option.value} value={option.value} sx={styles.option}>
          {option.label}
        </option>
      ))}
    </select>
  );
}

const drop = stylex.keyframes({
  from: { opacity: 0, transform: "translateY(-4px) scale(0.98)" },
  to: { opacity: 1, transform: "none" },
});

const styles = stylex.create({
  select: {
    appearance: "base-select",
    cursor: "pointer",
    "::picker-icon": { color: color.inkMuted },
    "::picker(select)": {
      appearance: "base-select",
      marginBlock: space.xs,
      padding: space.xs,
      backgroundColor: color.surface,
      borderWidth: 1.5,
      borderStyle: "solid",
      borderColor: color.ink,
      borderRadius: radius.md,
      boxShadow: `${shadow.inked}, ${shadow.card}`,
      color: color.ink,
      animationName: drop,
      animationDuration: motion.base,
      animationTimingFunction: motion.out,
    },
  },
  option: {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    gap: space.lg,
    paddingBlock: space.sm,
    paddingInline: space.md,
    borderRadius: radius.sm,
    fontSize: text.md,
    fontWeight: { default: null, ":checked": 600 },
    whiteSpace: "nowrap",
    color: { default: null, ":checked": color.accentStrong },
    outlineStyle: "none",
    backgroundColor: {
      default: "transparent",
      ":hover": color.sunken,
      ":focus-visible": color.accentSoft,
    },
    "::checkmark": { order: 1 },
  },
});
