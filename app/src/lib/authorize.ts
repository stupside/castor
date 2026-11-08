import "server-only";
import { headers } from "next/headers";
import { accessPolicy, authorized } from "./access";

// Enforce at the data boundary as well as Proxy: actions may move between routes.
export async function authorize() {
  if (!authorized(await headers(), accessPolicy()))
    throw new Error("Sign in to Castor to continue.");
}
