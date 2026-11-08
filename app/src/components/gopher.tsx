import * as stylex from "@stylexjs/stylex";
import { useId } from "react";
import { color, motion } from "./tokens.stylex";

// The README mascot, decorative; it blinks and, under a hovered stylex.defaultMarker(), presses its remote.
export function Gopher({ size = 40 }: { size?: number }) {
  const wobble = useId();
  return (
    <svg width={size} height={size} viewBox="0 0 100 100" aria-hidden>
      <filter id={wobble} x="-10%" y="-10%" width="120%" height="120%">
        <feTurbulence
          type="fractalNoise"
          baseFrequency="0.04"
          numOctaves="2"
          seed="3"
        />
        <feDisplacementMap
          in="SourceGraphic"
          scale="2.6"
          xChannelSelector="R"
          yChannelSelector="G"
        />
      </filter>
      <g
        filter={`url(#${wobble})`}
        stroke="#1f1a17"
        strokeWidth="1.3"
        strokeLinejoin="round"
        strokeLinecap="round"
      >
        <path
          d="M70 68C80 62 95 66 96 77C97 87 86 91 77 87C70 84 66 76 70 68Z"
          fill="#7a5638"
        />
        <path
          d="M74 70L92 84M84 65L95 75M90 68L76 86"
          stroke="#9c7552"
          strokeWidth="1"
        />
        <ellipse cx="39" cy="92" rx="7" ry="3.8" fill="#f3d3a6" />
        <ellipse cx="61" cy="92" rx="7" ry="3.8" fill="#f3d3a6" />
        <circle cx="32" cy="17" r="6.5" fill="#a5744d" />
        <circle cx="68" cy="17" r="6.5" fill="#a5744d" />
        <circle cx="32" cy="17" r="2.8" fill="#7a5638" stroke="none" />
        <circle cx="68" cy="17" r="2.8" fill="#7a5638" stroke="none" />
        <path
          d="M50 12C30 12 25 28 25 46C25 64 22 80 30 87C37 93 63 93 70 87C78 80 75 64 75 46C75 28 70 12 50 12Z"
          fill="#a5744d"
        />
        <ellipse cx="50" cy="76" rx="14" ry="11" fill="#ecd6b6" />
        <ellipse cx="50" cy="41" rx="19" ry="17" fill="#7fd3e6" />
        <ellipse
          cx="76"
          cy="60"
          rx="3.5"
          ry="6"
          transform="rotate(-20 76 60)"
          fill="#7fd3e6"
        />
        <g sx={styles.remote}>
          <rect
            x="12"
            y="40"
            width="7"
            height="15"
            rx="2"
            transform="rotate(-20 15.5 47.5)"
            fill="#3a3a3a"
          />
          <circle cx="14.5" cy="43" r="1.4" stroke="none" sx={styles.led} />
          <ellipse cx="22" cy="54" rx="4.5" ry="5" fill="#7fd3e6" />
        </g>
        <g sx={styles.eyes}>
          <circle cx="41" cy="36" r="7.5" fill="#ffffff" />
          <circle cx="60" cy="36" r="7.5" fill="#ffffff" />
          <circle cx="37.5" cy="35.5" r="2.7" fill="#1f1a17" />
          <circle cx="56.5" cy="35.5" r="2.7" fill="#1f1a17" />
        </g>
        <ellipse cx="50" cy="47.5" rx="6.5" ry="4.5" fill="#f3d3a6" />
        <ellipse cx="50" cy="45" rx="2.6" ry="1.8" fill="#1f1a17" />
        <path
          d="M47.5 51.5h2.5v3.5h-2.5ZM50 51.5h2.5v3.5h-2.5Z"
          fill="#ffffff"
          strokeWidth="0.9"
        />
      </g>
    </svg>
  );
}

const blink = stylex.keyframes({
  "0%, 94%, 100%": { transform: "scaleY(1)" },
  "97%": { transform: "scaleY(0.1)" },
});

const press = stylex.keyframes({
  "0%, 100%": { fill: color.danger },
  "50%": { fill: "#ff9a9d" },
});

const styles = stylex.create({
  eyes: {
    transformBox: "view-box",
    transformOrigin: "50px 36px",
    animationName: blink,
    animationDuration: "5s",
    animationIterationCount: "infinite",
  },
  remote: {
    transformBox: "view-box",
    transformOrigin: "22px 56px",
    transform: {
      default: "none",
      [stylex.when.ancestor(":hover")]: "rotate(-14deg) translateY(-3px)",
    },
    transitionProperty: "transform",
    transitionDuration: "320ms",
    transitionTimingFunction: motion.spring,
  },
  led: {
    fill: color.danger,
    animationName: {
      default: "none",
      [stylex.when.ancestor(":hover")]: press,
    },
    animationDuration: "500ms",
    animationIterationCount: "infinite",
  },
});
