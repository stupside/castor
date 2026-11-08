import { Code, ConnectError } from "@connectrpc/connect";

// The servers own validation; their message names the refused field, and a bare failure says its code.
export const describe = (e: unknown) =>
  e instanceof ConnectError
    ? e.rawMessage || Code[e.code].toLowerCase()
    : "unexpected error";
