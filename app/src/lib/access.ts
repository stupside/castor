import { createHash, timingSafeEqual } from "node:crypto";

type AccessPolicy = { password: string; trustedLAN: boolean };

// Independent web credentials never become the backend API credential.
export function accessPolicy(): AccessPolicy {
  return {
    password: process.env.CASTOR_WEB__PASSWORD || "",
    trustedLAN: process.env.CASTOR_WEB__TRUSTED_LAN === "true",
  };
}

export function authorized(
  headers: Pick<Headers, "get">,
  policy: AccessPolicy,
): boolean {
  if (policy.password) {
    const value = headers.get("authorization") || "";
    if (!/^Basic [A-Za-z0-9+/]+={0,2}$/i.test(value)) return false;
    const supplied = Buffer.from(value.slice(6), "base64");
    const expected = Buffer.from(`castor:${policy.password}`);
    const digest = (value: Uint8Array) =>
      createHash("sha256").update(value).digest();
    return timingSafeEqual(digest(supplied), digest(expected));
  }
  return policy.trustedLAN;
}

export function accessDenied(
  headers: Pick<Headers, "get">,
  policy = accessPolicy(),
): Response | undefined {
  if (authorized(headers, policy)) return;
  const configured = Boolean(policy.password);
  return new Response(
    configured
      ? "Sign in to Castor."
      : "Set CASTOR_WEB__PASSWORD, or explicitly enable CASTOR_WEB__TRUSTED_LAN=true on a trusted network.",
    {
      status: configured ? 401 : 503,
      headers: {
        "cache-control": "no-store",
        ...(configured
          ? { "www-authenticate": 'Basic realm="Castor", charset="UTF-8"' }
          : {}),
      },
    },
  );
}
