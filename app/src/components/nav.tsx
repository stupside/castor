"use client";

import * as stylex from "@stylexjs/stylex";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useLayoutEffect, useRef } from "react";
import { ButtonLink } from "./button";
import { Icon, type IconName } from "./icon";
import { color, media, motion, radius, space, text } from "./tokens.stylex";

const items = [
  {
    href: "/",
    label: "Casts",
    icon: "cast",
    active: (path: string) =>
      path === "/" || (path.startsWith("/casts/") && path !== "/casts/new"),
  },
  {
    href: "/devices",
    label: "Devices",
    icon: "device",
    active: (path: string) => path.startsWith("/devices"),
  },
] as const satisfies readonly {
  href: string;
  label: string;
  icon: IconName;
  active: (path: string) => boolean;
}[];

export function Nav() {
  const path = usePathname();
  const active = items.findIndex((item) => item.active(path));
  const list = useRef<HTMLUListElement>(null);
  const pill = useRef<HTMLSpanElement>(null);
  const placed = useRef(false);

  // A CSS transition, not a view transition: those paint the pill over the text and run on every refresh.
  useLayoutEffect(() => {
    const ul = list.current;
    const el = pill.current;
    if (!ul || !el) return;
    const place = (animate: boolean) => {
      const link = ul.querySelectorAll("a")[active];
      el.style.opacity = link ? "1" : "0";
      if (!link) return;
      if (!animate) el.style.transition = "none";
      el.style.transform = `translate(${link.offsetLeft}px, ${link.offsetTop}px)`;
      el.style.width = `${link.offsetWidth}px`;
      el.style.height = `${link.offsetHeight}px`;
      if (!animate) {
        void el.offsetWidth;
        el.style.transition = "";
      }
    };
    place(placed.current);
    placed.current = true;
    const resize = new ResizeObserver(() => place(false));
    resize.observe(ul);
    return () => resize.disconnect();
  }, [active]);

  return (
    <>
      <ButtonLink
        href="/casts/new"
        variant="primary"
        icon="plus"
        aria-current={path === "/casts/new" ? "page" : undefined}
      >
        New cast
      </ButtonLink>
      <nav aria-label="Main" sx={styles.nav}>
        <span ref={pill} aria-hidden sx={styles.pill} />
        <ul ref={list} sx={styles.list}>
          {items.map((item, i) => (
            <li key={item.href}>
              <Link
                href={item.href}
                aria-current={i === active ? "page" : undefined}
                {...stylex.props(styles.link, i === active && styles.here)}
              >
                <Icon name={item.icon} />
                {item.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
    </>
  );
}

const styles = stylex.create({
  nav: {
    position: "relative",
    flexBasis: { default: "100%", [media.wide]: "auto" },
  },
  list: {
    display: "flex",
    flexDirection: { default: "row", [media.wide]: "column" },
    gap: space.xxs,
    padding: 0,
    listStyle: "none",
  },
  link: {
    position: "relative",
    zIndex: 1,
    display: "flex",
    alignItems: "center",
    gap: space.sm,
    height: 40,
    paddingInline: space.md,
    borderRadius: radius.md,
    fontSize: text.md,
    fontWeight: 500,
    color: { default: color.inkSoft, ":hover": color.ink },
    textDecoration: "none",
    backgroundColor: { default: "transparent", ":hover": color.sunken },
    outlineStyle: { default: "none", ":focus-visible": "solid" },
    outlineWidth: 2,
    outlineColor: color.accentStrong,
  },
  here: {
    fontWeight: 600,
    color: color.accentStrong,
    backgroundColor: { default: "transparent", ":hover": "transparent" },
  },
  pill: {
    position: "absolute",
    top: 0,
    left: 0,
    opacity: 0,
    backgroundColor: color.accentSoft,
    borderRadius: radius.md,
    pointerEvents: "none",
    transitionProperty: "transform, width, height, opacity",
    transitionDuration: motion.slow,
    transitionTimingFunction: motion.spring,
  },
});
