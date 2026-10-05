"use client";

import { useRouter } from "next/navigation";
import { useEffect, useTransition } from "react";

export function AutoRefresh({ everyMs = 10000 }: { everyMs?: number }) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  useEffect(() => {
    const refresh = () => {
      if (!document.hidden && !pending) startTransition(() => router.refresh());
    };
    const id = setInterval(refresh, everyMs);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [router, everyMs, pending]);
  return null;
}
