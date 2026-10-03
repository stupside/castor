import { ConnectError } from "@connectrpc/connect";

// The servers own validation; their message names the refused field.
export const describe = (e: unknown) => (e instanceof ConnectError ? e.rawMessage : "unexpected error");
