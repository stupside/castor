// Reuses babel.config.json, so the CSS and the compiled components agree on every class.
const config = {
  plugins: {
    "@stylexjs/postcss-plugin": {
      include: ["app/**/*.{ts,tsx}", "src/**/*.{ts,tsx}"],
      exclude: ["src/gen/**"],
      babelConfig: {
        babelrc: false,
        parserOpts: { plugins: ["typescript", "jsx"] },
      },
      useCSSLayers: { before: ["reset"] },
    },
  },
};

export default config;
