import type {
  CompiledStyles,
  InlineStyles,
  StyleXArray,
} from "@stylexjs/stylex";

// StyleX compiles sx={...} into {...stylex.props(...)}, on host elements only.
declare module "react" {
  interface DOMAttributes<T> {
    sx?: StyleXArray<
      | CompiledStyles
      | Readonly<[CompiledStyles, InlineStyles]>
      | boolean
      | null
      | undefined
    >;
  }
}
