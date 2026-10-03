import "server-only";
import { casts } from "@/lib/server";

export const listCasts = async () => (await casts.listCasts({})).casts;
