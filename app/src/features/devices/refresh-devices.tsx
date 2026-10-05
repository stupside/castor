"use client";

import { useFormStatus } from "react-dom";
import { Button } from "@/components/button";
import { refreshDevicesAction } from "./actions";

function RefreshButton({ disabled }: { disabled: boolean }) {
  const { pending } = useFormStatus();
  return <Button variant="ghost" type="submit" disabled={disabled || pending} aria-busy={pending}>
    {pending ? "Looking for screens…" : "Refresh screens"}
  </Button>;
}

export function RefreshDevices({ disabled = false }: { disabled?: boolean }) {
  return <form action={refreshDevicesAction}><RefreshButton disabled={disabled} /></form>;
}
