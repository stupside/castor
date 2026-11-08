"use client";

import * as stylex from "@stylexjs/stylex";
import { useRef, useState } from "react";
import {
  color,
  font,
  motion,
  radius,
  shadow,
  space,
  text,
} from "./tokens.stylex";

// onChange follows every move; onCommit gets the value once, when a drag or a key press ends.
export function Slider({
  label,
  value,
  max,
  step,
  keyStep,
  pageStep,
  format,
  onChange,
  onCommit,
}: {
  label: string;
  value: number;
  max: number;
  step: number;
  keyStep: number;
  pageStep: number;
  format: (value: number) => string;
  onChange: (value: number) => void;
  onCommit: (value: number) => void;
}) {
  const thumb = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState(false);
  const [hover, setHover] = useState<number>();

  const percent = (v: number) => (max > 0 ? (v / max) * 100 : 0);
  const clamp = (v: number) =>
    Math.min(max, Math.max(0, Math.round(v / step) * step));
  // The track spans the whole slider, so the slider's box is the track's.
  const at = (e: React.PointerEvent<HTMLDivElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    return clamp(((e.clientX - box.left) / box.width) * max);
  };
  const keys: Record<string, number> = {
    ArrowRight: keyStep,
    ArrowUp: keyStep,
    ArrowLeft: -keyStep,
    ArrowDown: -keyStep,
    PageUp: pageStep,
    PageDown: -pageStep,
    Home: -max,
    End: max,
  };

  const release = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragging) return;
    setDragging(false);
    onCommit(at(e));
  };

  const bubble = dragging ? value : hover;

  return (
    <div
      sx={[styles.root, stylex.defaultMarker()]}
      onPointerDown={(e) => {
        if (e.button !== 0) return;
        e.currentTarget.setPointerCapture(e.pointerId);
        setDragging(true);
        setHover(undefined);
        onChange(at(e));
        thumb.current?.focus({ preventScroll: true });
      }}
      onPointerMove={(e) => {
        if (dragging) onChange(at(e));
        else if (e.pointerType === "mouse") setHover(at(e));
      }}
      onPointerUp={release}
      onPointerCancel={release}
      onPointerLeave={() => setHover(undefined)}
    >
      <div sx={styles.track}>
        {hover !== undefined && (
          <div sx={[styles.preview, styles.width(percent(hover))]} />
        )}
        <div
          sx={[
            styles.fill,
            styles.width(percent(value)),
            dragging && styles.instant,
          ]}
        />
      </div>
      <div
        ref={thumb}
        role="slider"
        tabIndex={0}
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={max}
        aria-valuenow={value}
        aria-valuetext={format(value)}
        onKeyDown={(e) => {
          const delta = keys[e.key];
          if (delta === undefined) return;
          e.preventDefault();
          onChange(clamp(value + delta));
        }}
        onKeyUp={(e) => {
          if (Object.hasOwn(keys, e.key)) onCommit(value);
        }}
        sx={[
          styles.thumb,
          styles.left(percent(value)),
          dragging && styles.grabbed,
        ]}
      />
      {bubble !== undefined && (
        <div aria-hidden sx={[styles.bubble, styles.left(percent(bubble))]}>
          {format(bubble)}
        </div>
      )}
    </div>
  );
}

const pop = stylex.keyframes({
  from: { opacity: 0, transform: "translate(-50%, 4px) scale(0.9)" },
  to: { opacity: 1, transform: "translate(-50%, 0)" },
});

const styles = stylex.create({
  root: {
    position: "relative",
    display: "flex",
    alignItems: "center",
    height: 32,
    cursor: "pointer",
    touchAction: "none",
    userSelect: "none",
  },
  track: {
    position: "relative",
    flexGrow: 1,
    height: {
      default: 6,
      [stylex.when.ancestor(":hover")]: 8,
    },
    overflow: "hidden",
    backgroundColor: color.sunken,
    borderRadius: radius.pill,
    boxShadow: `inset 0 0 0 1px ${color.line}`,
    transitionProperty: "height",
    transitionDuration: motion.fast,
  },
  preview: {
    position: "absolute",
    insetBlock: 0,
    left: 0,
    backgroundColor: color.lineStrong,
  },
  fill: {
    position: "absolute",
    insetBlock: 0,
    left: 0,
    backgroundColor: color.accent,
    borderRadius: radius.pill,
    transitionProperty: "width",
    transitionDuration: motion.base,
    transitionTimingFunction: motion.out,
  },
  instant: { transitionDuration: "0s" },
  width: (percent: number) => ({ width: `${percent}%` }),
  left: (percent: number) => ({ left: `${percent}%` }),
  thumb: {
    position: "absolute",
    top: "50%",
    width: 20,
    height: 20,
    backgroundColor: color.gopher,
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: color.ink,
    borderRadius: radius.pill,
    boxShadow: {
      default: shadow.inked,
      ":focus-visible": `${shadow.inked}, ${shadow.ring}`,
    },
    outlineStyle: "none",
    transform: {
      default: "translate(-50%, -50%)",
      [stylex.when.ancestor(":hover")]: "translate(-50%, -50%) scale(1.12)",
    },
    transitionProperty: "left, transform, box-shadow",
    transitionDuration: motion.base,
    transitionTimingFunction: motion.spring,
    cursor: "grab",
  },
  grabbed: {
    transform: "translate(-50%, -50%) scale(1.2)",
    transitionProperty: "transform, box-shadow",
    cursor: "grabbing",
  },
  bubble: {
    position: "absolute",
    bottom: "calc(100% + 4px)",
    paddingBlock: space.xs,
    paddingInline: space.sm,
    backgroundColor: color.ink,
    borderRadius: radius.sm,
    fontFamily: font.mono,
    fontSize: text.xs,
    fontVariantNumeric: "tabular-nums",
    color: color.surface,
    whiteSpace: "nowrap",
    pointerEvents: "none",
    transform: "translate(-50%, 0)",
    animationName: pop,
    animationDuration: motion.fast,
    animationTimingFunction: motion.out,
  },
});
