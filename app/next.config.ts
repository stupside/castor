import type { NextConfig } from "next";

// Phones and other machines open the dev server by LAN address; without this their pages render but never hydrate.
const config: NextConfig = { typedRoutes: true, allowedDevOrigins: ["192.168.*.*", "10.*.*.*", "*.local"] };

export default config;
