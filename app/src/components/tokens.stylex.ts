import * as stylex from "@stylexjs/stylex";

// The base hues come from the mascot; every shade mixes from them, so a theme overrides only the bases.
export const color = stylex.defineVars({
  surface: "#ffffff",
  ink: "#1f1a17",
  gopher: "#7fd3e6",
  gopherHover: "#5dc9e2",
  accent: "#00add8",
  success: "#00a29c",
  warning: "#fddd00",
  danger: "#e5484d",
  muzzle: "#f3d3a6",
  cream: "#fbf2e4",

  inkSoft: (): string =>
    `color-mix(in srgb, ${color.ink} 82%, ${color.surface})`,
  inkMuted: (): string =>
    `color-mix(in srgb, ${color.ink} 66%, ${color.surface})`,
  canvas: (): string =>
    `color-mix(in srgb, ${color.accent} 5%, ${color.surface})`,
  sunken: (): string =>
    `color-mix(in srgb, ${color.accent} 8%, ${color.surface})`,
  line: (): string =>
    `color-mix(in srgb, ${color.accentStrong} 14%, ${color.surface})`,
  lineStrong: (): string =>
    `color-mix(in srgb, ${color.accentStrong} 26%, ${color.surface})`,

  accentStrong: (): string =>
    `color-mix(in srgb, ${color.accent} 60%, ${color.ink})`,
  accentSoft: (): string =>
    `color-mix(in srgb, ${color.accent} 12%, ${color.surface})`,
  successStrong: (): string =>
    `color-mix(in srgb, ${color.success} 65%, ${color.ink})`,
  successSoft: (): string =>
    `color-mix(in srgb, ${color.success} 12%, ${color.surface})`,
  warningStrong: (): string =>
    `color-mix(in srgb, ${color.warning} 40%, ${color.ink})`,
  warningSoft: (): string =>
    `color-mix(in srgb, ${color.warning} 22%, ${color.surface})`,
  dangerStrong: (): string =>
    `color-mix(in srgb, ${color.danger} 75%, ${color.ink})`,
  dangerSoft: (): string =>
    `color-mix(in srgb, ${color.danger} 10%, ${color.surface})`,
});

export const font = stylex.defineVars({
  sans: "var(--font-work-sans), ui-sans-serif, system-ui, sans-serif",
  mono: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
});

export const shadow = stylex.defineVars({
  card: "0 1px 2px rgb(31 26 23 / 0.05), 0 2px 8px -4px rgb(0 84 107 / 0.12)",
  raised: "0 24px 48px -24px rgb(0 84 107 / 0.45)",
  // The ink line under drawn things, as the mascot's outline sits under it.
  inked: `0 2px 0 ${color.ink}`,
  inkedHigh: `0 3px 0 ${color.ink}`,
  ring: `0 0 0 3px ${color.accentSoft}`,
});

export const space = stylex.defineConsts({
  xxs: "2px",
  xs: "4px",
  sm: "8px",
  md: "12px",
  lg: "16px",
  xl: "24px",
  xxl: "32px",
  xxxl: "48px",
});

export const radius = stylex.defineConsts({
  sm: "6px",
  md: "10px",
  lg: "16px",
  pill: "999px",
});

export const text = stylex.defineConsts({
  xs: "12px",
  sm: "13px",
  md: "14px",
  lg: "16px",
  xl: "20px",
  xxl: "28px",
});

export const media = stylex.defineConsts({
  wide: "@media (min-width: 900px)",
});

export const motion = stylex.defineConsts({
  fast: "120ms",
  base: "220ms",
  slow: "420ms",
  out: "cubic-bezier(0.2, 0.8, 0.2, 1)",
  // Overshoots a little, like something drawn settling into place.
  spring: "cubic-bezier(0.3, 1.4, 0.5, 1)",
});
