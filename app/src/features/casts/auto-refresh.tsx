"use client";

import { useRouter } from "next/navigation";
import { useEffect, useEffectEvent, useTransition } from "react";

export function AutoRefresh() {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const refresh = useEffectEvent(() => {
    if (!document.hidden && !pending) startTransition(() => router.refresh());
  });
  useEffect(() => {
    const id = setInterval(refresh, 10_000);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, []);
  return null;
}
