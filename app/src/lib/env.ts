import "server-only";

export const env = {
  api: {
    url: process.env.CASTOR_SERVERS__API__URL || "http://localhost:8411",
    token: process.env.CASTOR_SERVERS__API__TOKEN || "",
  },
};
