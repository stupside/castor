"use client";

import * as stylex from "@stylexjs/stylex";
import { useSyncExternalStore } from "react";
import { Icon, type IconName } from "./icon";
import { color, motion, radius, shadow, space, text } from "./tokens.stylex";

type Tone = keyof typeof iconTones;
type Toast = { id: number; tone: Tone; message: string };

const lifetime = 4000;
const icons: Record<Tone, IconName> = {
  success: "check",
  danger: "alert",
};

let toasts: readonly Toast[] = [];
let next = 0;
const listeners = new Set<() => void>();
const none: readonly Toast[] = [];

function emit(list: readonly Toast[]) {
  toasts = list;
  for (const listener of listeners) listener();
}

// Callable from any client code; the Toaster in the root layout shows it.
export function toast(message: string, tone: Tone) {
  const id = next++;
  emit([...toasts, { id, tone, message }].slice(-4));
  setTimeout(() => emit(toasts.filter((t) => t.id !== id)), lifetime);
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function Toaster() {
  const list = useSyncExternalStore(
    subscribe,
    () => toasts,
    () => none,
  );
  return (
    <ol aria-live="polite" aria-label="Notifications" sx={styles.region}>
      {list.map((t) => (
        <li
          key={t.id}
          sx={[styles.toast, t.tone === "danger" && styles.danger]}
        >
          <span sx={[styles.icon, iconTones[t.tone]]}>
            <Icon name={icons[t.tone]} size={16} />
          </span>
          <span sx={styles.message}>{t.message}</span>
          <button
            type="button"
            aria-label="Dismiss"
            onClick={() => emit(toasts.filter((x) => x.id !== t.id))}
            sx={styles.close}
          >
            <Icon name="close" size={14} />
          </button>
        </li>
      ))}
    </ol>
  );
}

// In, hold, out: the whole life of a toast is one animation, so it leaves on its own.
const life = stylex.keyframes({
  "0%": { opacity: 0, transform: "translateY(12px) scale(0.96)" },
  "6%": { opacity: 1, transform: "none" },
  "92%": { opacity: 1, transform: "none" },
  "100%": { opacity: 0, transform: "translateY(4px) scale(0.98)" },
});

const styles = stylex.create({
  region: {
    position: "fixed",
    insetInlineEnd: space.lg,
    insetBlockEnd: space.lg,
    zIndex: 50,
    display: "grid",
    justifyItems: "end",
    gap: space.sm,
    maxWidth: "calc(100vw - 32px)",
    margin: 0,
    padding: 0,
    listStyle: "none",
    pointerEvents: "none",
  },
  toast: {
    display: "flex",
    alignItems: "center",
    gap: space.sm,
    maxWidth: 380,
    paddingBlock: space.sm,
    paddingInlineStart: space.sm,
    paddingInlineEnd: space.xs,
    backgroundColor: color.surface,
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: color.ink,
    borderRadius: radius.md,
    boxShadow: `${shadow.inked}, ${shadow.card}`,
    fontSize: text.md,
    color: color.ink,
    pointerEvents: "auto",
    animationName: life,
    animationDuration: `${lifetime}ms`,
    animationTimingFunction: motion.out,
    animationFillMode: "both",
  },
  icon: {
    display: "grid",
    placeItems: "center",
    flexShrink: 0,
    width: 26,
    height: 26,
    borderRadius: radius.pill,
  },
  danger: { borderColor: color.danger },
  message: { flexGrow: 1, minWidth: 0, overflowWrap: "anywhere" },
  close: {
    display: "grid",
    placeItems: "center",
    flexShrink: 0,
    width: 28,
    height: 28,
    padding: 0,
    backgroundColor: { default: "transparent", ":hover": color.sunken },
    borderWidth: 0,
    borderRadius: radius.pill,
    color: color.inkMuted,
  },
});

const iconTones = stylex.create({
  success: { backgroundColor: color.successSoft, color: color.successStrong },
  danger: { backgroundColor: color.dangerSoft, color: color.dangerStrong },
});
