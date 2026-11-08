"use client";

import { useState, useTransition } from "react";
import { Button } from "@/components/button";
import { toast } from "@/components/toast";
import { stopAction } from "./actions";

export function StopButton({ castId, size }: { castId: string; size?: "sm" }) {
  const [pending, startTransition] = useTransition();
  const [stopped, setStopped] = useState(false);
  return (
    <Button
      variant="danger"
      size={size}
      icon="stop"
      loading={pending}
      disabled={stopped}
      onClick={() =>
        startTransition(async () => {
          const error = await stopAction(castId);
          if (error) {
            toast(`Couldn’t stop: ${error}`, "danger");
          } else {
            setStopped(true);
            toast("Stop sent. The cast ends in a moment.", "success");
          }
        })
      }
    >
      {stopped ? "Stopping" : "Stop"}
    </Button>
  );
}
