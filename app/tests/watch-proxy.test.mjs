import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:http";
import test from "node:test";
import { gzipSync } from "node:zlib";
import { proxyWatch } from "../src/lib/watch-proxy.ts";

async function serve(t, handler) {
  const server = createServer(handler);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(async () => {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  });
  return `http://127.0.0.1:${server.address().port}`;
}

function request(options = {}) {
  return new Request("http://castor.test/rpc/api/castor.v1.CastService/Watch", {
    method: "POST", body: new Uint8Array([0, 0, 0, 0, 1, 7]),
    headers: { "content-type": "application/connect+proto", "connect-protocol-version": "1" }, ...options,
  });
}

test("the watch proxy relays bytes with server credentials and strips browser credentials and decoded HTTP headers", async (t) => {
  const payload = new Uint8Array([0, 0, 0, 0, 2, 8, 1]);
  const compressed = gzipSync(payload);
  let received;
  const url = await serve(t, async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    received = { path: req.url, headers: req.headers, body: Buffer.concat(chunks) };
    res.writeHead(200, {
      "content-type": "application/connect+proto", "content-encoding": "gzip", "content-length": compressed.length,
      "set-cookie": "api-session=private", "access-control-allow-origin": "*", "x-private": "secret",
    });
    res.end(compressed);
  });
  const response = await proxyWatch(request({ headers: {
    "content-type": "application/connect+proto", "connect-protocol-version": "1",
    authorization: "Bearer browser-token", cookie: "browser-session=private", "x-forwarded-host": "attacker.test",
    "accept-encoding": "gzip",
  } }), { url: `${url}/`, token: "server-token" });
  assert.deepEqual(new Uint8Array(await response.arrayBuffer()), payload);
  assert.equal(received.path, "/castor.v1.CastService/Watch");
  assert.equal(received.headers.authorization, "Bearer server-token");
  assert.equal(received.headers.cookie, undefined);
  assert.equal(received.headers["x-forwarded-host"], undefined);
  assert.equal(received.headers["accept-encoding"], "identity");
  assert.equal(received.headers["content-type"], "application/connect+proto");
  assert.equal(received.headers["connect-protocol-version"], "1");
  assert.deepEqual(received.body, Buffer.from([0, 0, 0, 0, 1, 7]));
  for (const name of ["set-cookie", "access-control-allow-origin", "content-encoding", "content-length", "x-private"]) {
    assert.equal(response.headers.get(name), null, name);
  }
  assert.equal(response.headers.get("content-type"), "application/connect+proto");
  assert.equal(response.headers.get("cache-control"), "no-store");
});

test("a watch redirect is refused before credentials reach another endpoint", async (t) => {
  const paths = [];
  const url = await serve(t, (req, res) => {
    paths.push(req.url);
    if (req.url === "/castor.v1.CastService/Watch") res.writeHead(307, { location: "/other" });
    res.end();
  });
  const response = await proxyWatch(request(), { url, token: "server-token" });
  assert.equal(response.status, 502);
  assert.deepEqual(paths, ["/castor.v1.CastService/Watch"]);
  assert.equal((await response.json()).code, "unavailable");
});

for (const cancelVia of ["response body", "request signal"]) {
  test(`cancelling the ${cancelVia} closes a quiet upstream watch`, { timeout: 5000 }, async (t) => {
    let closed;
    const disconnected = new Promise((resolve) => { closed = resolve; });
    const url = await serve(t, (req, res) => {
      res.once("close", closed);
      res.writeHead(200, { "content-type": "application/connect+proto" });
      res.write(new Uint8Array([0, 0, 0, 0, 0]));
    });
    const abort = new AbortController();
    const response = await proxyWatch(request({ signal: abort.signal }), { url, token: "" });
    const reader = response.body.getReader();
    assert.equal((await reader.read()).done, false);
    if (cancelVia === "response body") await reader.cancel();
    else {
      abort.abort();
      await assert.rejects(reader.read());
    }
    await disconnected;
  });
}

test("an unavailable API returns a usable error without exposing the server token", async (t) => {
  const url = await serve(t, (req) => req.socket.destroy());
  const response = await proxyWatch(request({ headers: { authorization: "Bearer browser-token" } }), { url, token: "secret-server-token" });
  assert.equal(response.status, 502);
  const body = await response.json();
  assert.equal(body.code, "unavailable");
  assert.equal(JSON.stringify(body).includes("secret-server-token"), false);
});
