#!/usr/bin/env bun
// Publish the castor launcher and one binary package per platform to npm; run from the repo root in CI.
import { spawnSync } from "node:child_process";
import { chmodSync, copyFileSync, globSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { z } from "zod";

const Manifest = z.object({
  name: z.string(),
  version: z.string(),
  description: z.string(),
  keywords: z.array(z.string()).optional(),
  license: z.literal("MIT"),
  repository: z.object({ type: z.literal("git"), url: z.string() }),
  os: z.array(z.string()).optional(),
  cpu: z.array(z.string()).optional(),
  libc: z.array(z.string()).optional(),
  bin: z.record(z.string(), z.string()).optional(),
  engines: z.record(z.string(), z.string()).optional(),
  optionalDependencies: z.record(z.string(), z.string()).optional(),
});
type Manifest = z.input<typeof Manifest>;

const version = z.string().min(1).parse(process.env.VERSION).replace(/^v/, "");
const scope = "@castor-dev";
const repository = { type: "git", url: "git+https://github.com/stupside/castor.git" } as const;
const out = mkdtempSync(join(tmpdir(), "castor-npm-"));

// npm only ships README and LICENSE on its own, so the docs people look for on the package page are listed.
const docs = Object.fromEntries(["LICENSE", "README.md", "CHANGELOG.md", "ARCHITECTURE.md", "CONTRIBUTING.md", "SECURITY.md"].map((f) => [f, f]));

const sh = (program: string, ...args: string[]) => spawnSync(program, args, { stdio: "inherit" }).status === 0;
const must = (ok: boolean, what: string) => {
  if (!ok) throw new Error(`${what} failed`);
};

function stage(dir: string, manifest: Manifest, files: Record<string, string>) {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "package.json"), JSON.stringify(Manifest.parse(manifest), null, 2));
  for (const [src, dst] of Object.entries(files)) copyFileSync(src, join(dir, dst));
}

const packages: { dir: string; name: string }[] = [];
for (const dir of globSync("prebuilt/castor-*")) {
  // npm names platforms the Node way: win32 and x64 where Go says windows and amd64.
  const [goos, goarch] = basename(dir).replace("castor-", "").split("-");
  const os = goos === "windows" ? "win32" : goos!;
  const cpu = goarch === "amd64" ? "x64" : goarch!;
  const exe = os === "win32" ? "castor.exe" : "castor";
  const name = `${scope}/cli-${os}-${cpu}`;
  const pkg = join(out, `cli-${os}-${cpu}`);
  stage(
    pkg,
    {
      name, version, description: `castor binary for ${os}-${cpu}`, license: "MIT", repository,
      os: [os], cpu: [cpu],
      // The Linux builds link glibc, so musl installs must skip them instead of getting a binary that cannot run.
      libc: os === "linux" ? ["glibc"] : undefined,
    },
    { [join(dir, "castor")]: exe, ...docs },
  );
  chmodSync(join(pkg, exe), 0o755);
  packages.push({ dir: pkg, name });
}

const launcher = { dir: join(out, "castor"), name: `${scope}/cli` };
// tsc is the type check: a launcher that does not compile never ships.
must(sh("bun", "install", "--cwd", "scripts/npm", "--frozen-lockfile"), "bun install");
must(sh("scripts/npm/node_modules/.bin/tsc", "-p", "scripts/npm"), "tsc");
must(sh("bun", "build", "scripts/npm/launcher.ts", "--target=node", "--format=cjs", "--outfile", join(launcher.dir, "bin/castor.js")), "bun build");
stage(
  launcher.dir,
  {
    name: launcher.name, version, description: "Cast video from any web page to Chromecast, DLNA, Roku and Samsung TVs, with live transcoding and subtitles", license: "MIT", repository,
    // The repo topics whose npm results are already about casting media; streaming, video and live there are Node streams and dev tooling.
    keywords: ["castor", "chromecast", "google-cast", "dlna", "roku", "samsung", "iptv", "hls", "m3u8", "termux", "self-hosted"],
    bin: { castor: "bin/castor.js" }, engines: { node: ">=20" },
    optionalDependencies: Object.fromEntries(packages.map(({ name }) => [name, version])),
  },
  docs,
);

// A prerelease must not become `latest`; platform packages first so the launcher never resolves to nothing.
const tag = version.includes("-") ? "canary" : "latest";
for (const { dir, name } of [...packages, launcher]) {
  // A re-run after a partial failure skips what already shipped, since npm refuses to republish a version.
  if (sh("npm", "view", `${name}@${version}`, "version")) {
    console.log(`skip ${name}@${version}`);
    continue;
  }
  must(sh("npm", "publish", dir, "--access", "public", "--tag", tag), `publish ${name}`);
}
