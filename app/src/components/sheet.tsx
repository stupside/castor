"use client";

import * as stylex from "@stylexjs/stylex";
import { useRouter } from "next/navigation";
import { useEffect, useId, useRef } from "react";
import { Button } from "./button";
import { color, media, motion, shadow, space, text } from "./tokens.stylex";

// A route shown over the page it was opened from (an intercepted route); closing goes back.
export function Sheet({
  title,
  description,
  children,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  children: React.ReactNode;
}) {
  const router = useRouter();
  const dialog = useRef<HTMLDialogElement>(null);
  const titleId = useId();

  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, []);

  return (
    <dialog
      ref={dialog}
      aria-labelledby={titleId}
      onCancel={(e) => {
        e.preventDefault();
        router.back();
      }}
      onClick={(e) => {
        if (e.target === dialog.current) router.back();
      }}
      sx={styles.sheet}
    >
      <div sx={styles.panel}>
        <header sx={styles.header}>
          <div sx={styles.heading}>
            <h2 id={titleId} sx={styles.title}>
              {title}
            </h2>
            {description && <p sx={styles.description}>{description}</p>}
          </div>
          <Button
            variant="ghost"
            size="icon"
            icon="close"
            aria-label="Close"
            onClick={() => router.back()}
          />
        </header>
        <div sx={styles.body}>{children}</div>
      </div>
    </dialog>
  );
}

const slideIn = stylex.keyframes({
  from: { transform: "translateX(48px)", opacity: 0 },
  to: { transform: "none", opacity: 1 },
});

const fadeIn = stylex.keyframes({ from: { opacity: 0 } });

const styles = stylex.create({
  sheet: {
    insetInlineStart: "auto",
    insetInlineEnd: 0,
    insetBlock: 0,
    width: { default: "100vw", [media.wide]: "min(640px, 100vw)" },
    maxWidth: "100vw",
    height: "100dvh",
    maxHeight: "100dvh",
    margin: 0,
    padding: 0,
    backgroundColor: color.canvas,
    borderWidth: 0,
    borderInlineStartWidth: { default: 0, [media.wide]: 1.5 },
    borderStyle: "solid",
    borderColor: color.ink,
    boxShadow: shadow.raised,
    color: color.ink,
    animationName: slideIn,
    animationDuration: motion.slow,
    animationTimingFunction: motion.out,
    "::backdrop": {
      backgroundColor: `color-mix(in srgb, ${color.ink} 32%, transparent)`,
      backdropFilter: "blur(2px)",
      animationName: fadeIn,
      animationDuration: motion.base,
    },
  },
  panel: { display: "flex", flexDirection: "column", minHeight: "100%" },
  header: {
    position: "sticky",
    top: 0,
    zIndex: 1,
    display: "flex",
    alignItems: "flex-start",
    justifyContent: "space-between",
    gap: space.md,
    paddingBlock: space.lg,
    paddingInline: space.xl,
    backgroundColor: color.surface,
    borderBottomWidth: 1,
    borderBottomStyle: "solid",
    borderBottomColor: color.line,
  },
  heading: { display: "grid", gap: space.xxs, minWidth: 0 },
  title: {
    fontSize: text.xl,
    fontWeight: 700,
    letterSpacing: "-0.02em",
  },
  description: { fontSize: text.sm, color: color.inkMuted },
  body: { flexGrow: 1, padding: space.xl },
});
