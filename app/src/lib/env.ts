import "server-only";

const read = (key: string, fallback: string) => process.env[key] || fallback;

export const env = {
  api: { url: read("CASTOR_API__URL", "http://localhost:8411"), token: read("CASTOR_API__TOKEN", "") },
};
