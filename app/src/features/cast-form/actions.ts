"use server";

import { redirect } from "next/navigation";
import { CastRequestSchema, CastResponseSchema } from "@/gen/castor/v1/cast_pb";
import { describe } from "@/lib/errors";
import { fromForm } from "@/lib/form";
import { casts } from "@/lib/server";
import { valid } from "@/lib/valid";

// values refill the form after a refusal, which resets it.
type FormState = { error?: string; values?: Record<string, string> };

export async function submitAction(
  _: FormState,
  f: FormData,
): Promise<FormState> {
  let castId: string;
  try {
    ({ castId } = valid(
      CastResponseSchema,
      await casts.cast(fromForm(CastRequestSchema, f)),
    ));
  } catch (e) {
    return {
      error: describe(e),
      values: Object.fromEntries([...f].map(([k, v]) => [k, String(v)])),
    };
  }
  redirect(`/casts/${encodeURIComponent(castId)}`);
}
