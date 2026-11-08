import * as stylex from "@stylexjs/stylex";
import Link from "next/link";
import { Icon, type IconName } from "./icon";
import { color, motion, radius, shadow, space, text } from "./tokens.stylex";

type Look = {
  variant?: keyof typeof variants;
  size?: keyof typeof sizes;
  icon?: IconName;
};

const iconSize = (size: keyof typeof sizes) => (size === "md" ? 18 : 16);

// A loading button keeps its width and swaps its icon for a spinner.
export function Button({
  variant = "secondary",
  size = "md",
  icon,
  loading,
  disabled,
  children,
  sx,
  ...props
}: Look & { loading?: boolean } & React.ComponentProps<"button">) {
  return (
    <button
      {...props}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      sx={[styles.base, variants[variant], sizes[size], sx]}
    >
      {loading ? (
        <span
          aria-hidden
          sx={[styles.spinner, styles.size(iconSize(size) - 2)]}
        />
      ) : (
        icon && <Icon name={icon} size={iconSize(size)} />
      )}
      {children}
    </button>
  );
}

// A link that looks like a Button; next/link is no host element, so it takes stylex.props.
export function ButtonLink<Route>({
  variant = "secondary",
  size = "md",
  icon,
  children,
  sx,
  ...props
}: Look & React.ComponentProps<typeof Link<Route>>) {
  return (
    <Link<Route>
      {...props}
      {...stylex.props(styles.base, variants[variant], sizes[size], sx)}
    >
      {icon && <Icon name={icon} size={iconSize(size)} />}
      {children}
    </Link>
  );
}

const spin = stylex.keyframes({ to: { transform: "rotate(1turn)" } });

const styles = stylex.create({
  spinner: {
    display: "inline-block",
    flexShrink: 0,
    borderWidth: 2,
    borderStyle: "solid",
    borderColor: "currentColor",
    borderInlineEndColor: "transparent",
    borderRadius: radius.pill,
    animationName: spin,
    animationDuration: "700ms",
    animationTimingFunction: "linear",
    animationIterationCount: "infinite",
  },
  size: (size: number) => ({ width: size, height: size }),
  base: {
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    gap: space.sm,
    flexShrink: 0,
    borderWidth: 1.5,
    borderStyle: "solid",
    borderColor: "transparent",
    borderRadius: radius.pill,
    fontWeight: 600,
    lineHeight: 1,
    whiteSpace: "nowrap",
    textDecoration: "none",
    userSelect: "none",
    transitionProperty:
      "background-color, border-color, color, box-shadow, transform",
    transitionDuration: motion.fast,
    transitionTimingFunction: motion.out,
    opacity: { default: 1, ":disabled": 0.5 },
    transform: {
      default: null,
      ":hover:not(:disabled)": "translateY(-1px)",
      ":active:not(:disabled)": "translateY(1px)",
    },
    outlineStyle: { default: "none", ":focus-visible": "solid" },
    outlineWidth: 2,
    outlineOffset: 2,
    outlineColor: color.accentStrong,
  },
});

const variants = stylex.create({
  // Drawn like the mascot: gopher fill, ink outline, ink line under it.
  primary: {
    backgroundColor: {
      default: color.gopher,
      ":hover:not(:disabled)": color.gopherHover,
    },
    borderColor: color.ink,
    color: color.ink,
    boxShadow: {
      default: shadow.inked,
      ":hover:not(:disabled)": shadow.inkedHigh,
      ":active:not(:disabled)": "none",
      ":disabled": "none",
    },
    transform: {
      default: null,
      ":hover:not(:disabled)": "translateY(-1px)",
      ":active:not(:disabled)": "translateY(2px)",
    },
  },
  secondary: {
    backgroundColor: {
      default: color.surface,
      ":hover:not(:disabled)": color.sunken,
    },
    borderColor: {
      default: color.lineStrong,
      ":hover:not(:disabled)": color.accent,
    },
    color: color.ink,
  },
  ghost: {
    backgroundColor: {
      default: "transparent",
      ":hover:not(:disabled)": color.sunken,
    },
    color: { default: color.inkSoft, ":hover:not(:disabled)": color.ink },
  },
  danger: {
    backgroundColor: {
      default: color.surface,
      ":hover:not(:disabled)": color.dangerSoft,
    },
    borderColor: {
      default: color.lineStrong,
      ":hover:not(:disabled)": color.danger,
    },
    color: color.dangerStrong,
  },
});

const sizes = stylex.create({
  md: { height: 40, paddingInline: space.lg, fontSize: text.md },
  sm: { height: 32, paddingInline: space.md, fontSize: text.sm },
  icon: { width: 32, height: 32, padding: 0, fontSize: text.sm },
});
