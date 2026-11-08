"use client";

import { useState } from "react";
import { Button } from "./button";
import { toast } from "./toast";

export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      variant="ghost"
      size="icon"
      icon={copied ? "check" : "copy"}
      aria-label={label}
      title={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
          toast("Copied to the clipboard", "success");
        } catch {
          toast("The browser refused the clipboard", "danger");
        }
      }}
    />
  );
}
