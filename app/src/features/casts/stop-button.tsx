"use client";

import { useActionState } from "react";
import { Button, type Variant } from "@/components/button";
import { stopAction } from "./actions";

export function StopButton({ castId, variant = "stop" }: { castId: string; variant?: Variant }) {
  const [state, action, pending] = useActionState(stopAction.bind(null, castId), {});
  return <form action={action} className="max-w-xs space-y-2">
    <Button variant={variant} disabled={pending || state.stopped} aria-busy={pending}>
      {pending ? "Stopping…" : state.stopped ? "Stop requested" : "Stop"}
    </Button>
    {state.error && <p role="alert" className="text-xs font-semibold text-red-300">Couldn’t stop: {state.error}</p>}
    {state.stopped && <span role="status" className="sr-only">Stop requested. Waiting for the cast to end.</span>}
  </form>;
}
