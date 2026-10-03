import { createConnectTransport } from "@connectrpc/connect-web";

// The proxy under app/rpc adds the token; the browser never holds it.
export const browserTransport = (server: "api" | "media") =>
  createConnectTransport({ baseUrl: `/rpc/${server}` });
