import next from "eslint-config-next";

const config = [...next, { ignores: ["src/gen/**", ".next/**"] }];

export default config;
