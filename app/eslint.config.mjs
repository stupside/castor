import stylex from "@stylexjs/eslint-plugin";
import next from "eslint-config-next";

const config = [
  ...next,
  {
    plugins: { "@stylexjs": stylex },
    rules: {
      "@stylexjs/valid-styles": "error",
      "@stylexjs/valid-shorthands": "error",
      "@stylexjs/no-unused": "error",
      "@stylexjs/no-conflicting-props": "error",
      "@stylexjs/enforce-extension": [
        "error",
        { themeFileExtension: ".stylex.ts" },
      ],
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "JSXOpeningElement[name.name='Link'] > JSXAttribute[name.name='sx']",
          message:
            "next/link drops sx (it compiles only on host elements); use ButtonLink or spread stylex.props(...).",
        },
      ],
    },
  },
  {
    // Customizable select (base-select, ::picker, ::checkmark) is newer than the plugin's lists; the compiler knows it.
    files: ["src/components/select.tsx"],
    rules: { "@stylexjs/valid-styles": "off" },
  },
  { ignores: ["src/gen/**", ".next/**"] },
];

export default config;
