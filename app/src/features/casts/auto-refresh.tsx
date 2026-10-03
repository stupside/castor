"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

export function AutoRefresh({ everyMs = 10000 }: { everyMs?: number }) {
  const router = useRouter();
  useEffect(() => {
    const id = setInterval(router.refresh, everyMs);
    return () => clearInterval(id);
  }, [router, everyMs]);
  return null;
}
