"use client";

export default function Error({ error, reset }: { error: Error; reset: () => void }) {
  return (
    <p className="text-sm text-red-600">
      {error.message} <button className="underline" onClick={reset}>Retry</button>
    </p>
  );
}
