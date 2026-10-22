import { after, before, test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir, writeFile, symlink, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { WebSocket } from "ws";
import { JSDOM } from "jsdom";

let frontend, upstream, origin, media, temp, exitPromise;
let upstreamError = false;
const received = [];
let stderr = "";
const image = { path: "alice/tweet/photo.jpg", tags: [{ tag: "cat", confidence: 0.9 }] };

before(async () => {
  temp = await mkdtemp(path.join(tmpdir(), "xmedia-node-"));
  media = path.join(temp, "media");
  await mkdir(path.join(media, "alice/tweet"), { recursive: true });
  await writeFile(path.join(media, image.path), "image-content");
  await writeFile(path.join(temp, "private.txt"), "secret");
  await writeFile(path.join(media, ".hidden"), "secret");
  await symlink(path.join(temp, "private.txt"), path.join(media, "outside.jpg"));
  upstream = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    received.push({ method: req.method, url: req.url, body: Buffer.concat(chunks).toString() });
    const url = new URL(req.url, "http://localhost");
    res.setHeader("Content-Type", "application/json");
    if (upstreamError) { res.writeHead(503).end('{"error":"unavailable"}'); return; }
    if (req.method !== "GET") {
      res.writeHead(202).end(JSON.stringify({ success: true })); return;
    }
    let data = { items: [], current_page: 2, total_pages: 3, total_items: 201, per_page: 100 };
    if (url.pathname === "/api/images") data.items = [image];
    if (url.pathname === "/api/users") data.items = [{ username: "alice", tweet_count: 1 }];
    if (url.pathname === "/api/tags") data.items = [{ tag: "cat", count: 1 }];
    if (url.pathname.endsWith("/tweets")) data.items = [{ tweet_id: "tweet", images: [image] }];
    if (url.pathname.startsWith("/api/autotag/")) data = { state: "PROGRESS", current: 1, total: 2 };
    if (url.pathname === "/api/download") data = { queue_depth: 1, items: [] };
    res.end(JSON.stringify(data));
  });
  upstream.listen(0, "127.0.0.1");
  await once(upstream, "listening");
  const api = `http://127.0.0.1:${upstream.address().port}`;
  frontend = spawn(process.execPath, ["dist/server/server.js", "--production"], {
    env: { ...process.env, HOST: "127.0.0.1", PORT: "0", MEDIA_ROOT: media, API_BASE_URL: api, ASYNQ_API_BASE_URL: api },
    stdio: ["ignore", "pipe", "pipe"],
  });
  exitPromise = once(frontend, "exit");
  frontend.stderr.on("data", (data) => { stderr += data; });
  origin = await new Promise((resolve, reject) => {
    const deadline = setTimeout(() => reject(new Error(`Startup timed out: ${stderr}`)), 10_000);
    frontend.once("exit", (code) => { clearTimeout(deadline); reject(new Error(`Startup failed (${code}): ${stderr}`)); });
    frontend.stdout.on("data", (data) => {
      const match = String(data).match(/listening on port (\d+)/);
      if (match) { clearTimeout(deadline); resolve(`http://127.0.0.1:${match[1]}`); }
    });
  });
});

after(async () => {
  if (frontend && frontend.exitCode === null) {
    frontend.kill("SIGTERM");
    const [code, signal] = await exitPromise;
    assert.equal(code, 0, stderr);
    assert.equal(signal, null);
  }
  if (upstream) await new Promise((resolve) => upstream.close(resolve));
  if (temp) await rm(temp, { recursive: true, force: true });
});

function pageState(html) {
  const json = html.match(/<script id="page-data" type="application\/json">(.*?)<\/script>/s)?.[1];
  assert.ok(json, "serialized hydration state");
  return JSON.parse(json);
}

test("SSR renders all pages, preserves filters and loads built assets", async () => {
  for (const [url, page, title] of [
    ["/?page=2", "home", "Home"],
    ["/users?min_tag_count=2", "users", "Users"],
    ["/users/alice?exclude_tags=dog", "userTweets", "alice"],
    ["/tags?name_contains=cat", "tags", "Tags"],
    ["/tags/cat?max_tag_count=3", "tagImages", "Tag: cat"],
    ["/download-status", "downloadStatus", "Download Status"],
    ["/autotag-status", "autotagStatus", "Autotagger Status"],
  ]) {
    const response = await fetch(origin + url);
    assert.equal(response.status, 200, url);
    const html = await response.text();
    assert.match(html, new RegExp(`<title>[^<]*${title}[^<]*</title>`));
    assert.match(html, /X Gallery/);
    const state = pageState(html);
    assert.equal(state.page, page);
    assert.equal(state.search, new URL(url, origin).search);
    assert.equal(response.headers.get("x-powered-by"), null);
    if (page === "userTweets") assert.match(html, /value="dog"/);
    const asset = html.match(/src="(\/assets\/[^\"]+\.js)"/)[1];
    const script = await fetch(origin + asset);
    assert.equal(script.status, 200);
    assert.match(script.headers.get("cache-control"), /immutable/);
    const js = await script.text();
    assert.doesNotMatch(js, /process\.env|Deno|queue-api/);
  }
  assert.equal((await fetch(origin + "/styles.css")).status, 200);
  const missing = await fetch(origin + "/missing");
  assert.equal(missing.status, 404);
  assert.equal(pageState(await missing.text()).page, "notFound");
  assert.equal((await fetch(origin + "/api/missing")).status, 404);
});

test("dynamic parameters are decoded once and hydration JSON cannot close its script", async () => {
  const tag = 'cat%20</script><script>alert("x")</script>$&';
  const response = await fetch(`${origin}/tags/${encodeURIComponent(tag)}`);
  const html = await response.text();
  assert.equal(pageState(html).data.tag, tag);
  assert.equal(new URL(received.at(-1).url, origin).searchParams.get("tags"), tag);
  assert.doesNotMatch(html, /<script>alert/);
  assert.match(html, /\\u003c\/script>/);
});

test("API proxy preserves query strings, JSON bodies, methods and upstream status", async () => {
  for (const [method, url, body] of [
    ["GET", "/api/images?page=2&tags=cat", undefined],
    ["GET", "/api/users?page=2", undefined],
    ["GET", "/api/tags?name_contains=cat", undefined],
    ["GET", "/api/users/alice/tweets?page=2", undefined],
    ["GET", "/api/tasks/status?id=123", undefined],
    ["GET", "/api/download?page=2", undefined],
    ["GET", "/api/autotag/status", undefined],
    ["GET", "/api/autotag/retag-status", undefined],
    ["POST", "/api/download", { urls: ["https://x.com/alice/status/1"] }],
    ["DELETE", "/api/users", { username: "alice" }],
    ["DELETE", "/api/tags", { tag: "cat" }],
    ["DELETE", "/api/images", { filepath: image.path }],
    ["POST", "/api/images/bulk-delete", { filepaths: [image.path] }],
    ["POST", "/api/images/retag", { filepath: image.path }],
    ["POST", "/api/images/retag-bulk", { filepaths: [image.path] }],
    ["POST", "/api/autotag/reload", undefined],
    ["POST", "/api/autotag/untagged", undefined],
    ["POST", "/api/autotag/reconcile", undefined],
  ]) {
    const response = await fetch(origin + url, {
      method, headers: { "Content-Type": "application/json" }, body: body && JSON.stringify(body),
    });
    assert.equal(response.status, method === "GET" ? 200 : 202, `${method} ${url}`);
    assert.equal(received.at(-1).method, method);
    assert.equal(received.at(-1).url, url === "/api/images/retag-bulk" ? "/api/images/retag/bulk" : url);
    assert.equal(received.at(-1).body, body ? JSON.stringify(body) : "");
    await response.arrayBuffer();
  }
  assert.equal((await fetch(origin + "/api/images", { method: "PUT" })).status, 405);
  assert.equal((await fetch(origin + "/api/autotag/invalid")).status, 404);
  assert.equal((await fetch(origin + "/", { method: "POST" })).status, 405);
  upstreamError = true;
  try {
    assert.equal((await fetch(origin + "/api/images")).status, 503);
    const fallback = await fetch(origin + "/");
    assert.equal(fallback.status, 200);
    assert.deepEqual(pageState(await fallback.text()).data.images, []);
  } finally { upstreamError = false; }
});

test("images support streaming, HEAD, conditional GET and ranges, with path containment", async () => {
  const url = origin + "/images/" + image.path;
  const response = await fetch(url);
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type"), /image\/jpeg/);
  assert.equal(await response.text(), "image-content");
  assert.equal((await fetch(url, { headers: { "If-None-Match": response.headers.get("etag"), "Cache-Control": "max-age=0" } })).status, 304);
  const head = await fetch(url, { method: "HEAD" });
  assert.equal(head.status, 200);
  assert.equal(await head.text(), "");
  const range = await fetch(url, { headers: { Range: "bytes=0-4" } });
  assert.equal(range.status, 206);
  assert.equal(await range.text(), "image");
  for (const [pathname, status] of [
    ["/images/missing.jpg", 404], ["/images/outside.jpg", 400],
    ["/images/%2e%2e%2fprivate.txt", 400], ["/images/.hidden", 403],
    ["/images/%E0%A4%A", 400], ["/images/%5c..%5cprivate.txt", 400], ["/images/%00.jpg", 400],
  ]) assert.equal((await fetch(origin + pathname)).status, status, pathname);
});

test("WebSocket sends live status and cleans up disconnected clients", async () => {
  assert.equal((await fetch(origin + "/api/ws/status")).status, 426);
  const socket = new WebSocket(origin.replace("http:", "ws:") + "/api/ws/status");
  const messages = [];
  socket.on("message", (data) => messages.push(JSON.parse(String(data))));
  await once(socket, "open");
  await new Promise((resolve, reject) => {
    const deadline = setTimeout(() => reject(new Error("Missing repeated status update")), 6000);
    socket.on("message", () => { if (messages.length >= 2) { clearTimeout(deadline); resolve(); } });
  });
  assert.equal(messages[0].type, "status");
  assert.equal(messages[0].locks.autotagBusy, true);
  assert.equal(messages[0].locks.retagBusy, true);
  assert.equal(messages[0].download.queue_depth, 1);
  assert.ok(messages[1].ts >= messages[0].ts);
  socket.close();
  await once(socket, "close");
});


test("built client hydrates filters, opens modals and submits downloads", async () => {
  for (const route of ["/", "/users/alice?exclude_tags=dog"]) {
    const html = await (await fetch(origin + route)).text();
    const js = await (await fetch(origin + html.match(/src="(\/assets\/[^\"]+\.js)"/)[1])).text();
    const dom = new JSDOM(html, { url: origin + route, runScripts: "outside-only", pretendToBeVisual: true });
    const sockets = [];
    const errors = [];
    dom.window.console.error = (...args) => errors.push(args);
    dom.window.fetch = (url, options) => fetch(new URL(url, origin), options);
    dom.window.WebSocket = class extends WebSocket {
      constructor(url) { super(url); this.on("error", () => {}); sockets.push(this); }
    };
    const waitFor = async (condition) => {
      const deadline = Date.now() + 3000;
      while (!condition()) {
        if (Date.now() >= deadline) assert.fail(`Client update timed out: ${JSON.stringify(errors)}`);
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
    };
    try {
      const doc = dom.window.document;
      const rootBeforeHydration = doc.querySelector(".container");
      const searchBeforeHydration = doc.querySelector('input[placeholder="Search by tag"]');
      searchBeforeHydration.value = "typed before hydration";
      searchBeforeHydration.focus();
      dom.window.eval(js);
      await waitFor(() => sockets.length > 0);
      assert.equal(doc.querySelector(".container"), rootBeforeHydration, "SSR nodes are reused");
      assert.equal(doc.activeElement, searchBeforeHydration, "input focus survives hydration");
      assert.equal(searchBeforeHydration.value, "typed before hydration", "typed input survives hydration");
      if (route.includes("exclude_tags")) {
        assert.ok([...doc.querySelectorAll("input")].some((input) => input.value === "dog"));
      }
      doc.querySelector("button.img-container").click();
      await waitFor(() => doc.querySelector(".modal-overlay.visible"));
      doc.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      await waitFor(() => !doc.querySelector(".modal-overlay.visible"));
      const textarea = doc.querySelector("#tweetUrls");
      textarea.value = "https://x.com/alice/status/1";
      textarea.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, 0));
      const requestCount = received.length;
      doc.querySelector("#downloadBtn").click();
      await waitFor(() => received.slice(requestCount).some((req) => req.method === "POST" && req.url === "/api/download" && req.body.includes("alice/status/1")));
      assert.deepEqual(errors, []);
    } finally {
      for (const socket of sockets) socket.terminate();
      dom.window.close();
    }
  }
});


test("fatal startup errors use stderr and exit 1", async () => {
  const child = spawn(process.execPath, ["dist/server/server.js", "--production"], {
    env: { ...process.env, PORT: "invalid" }, stdio: ["ignore", "pipe", "pipe"],
  });
  let out = "", err = "";
  child.stdout.on("data", (data) => { out += data; });
  child.stderr.on("data", (data) => { err += data; });
  const [code] = await once(child, "exit");
  assert.equal(code, 1);
  assert.match(err, /FATAL Failed to start frontend/);
  assert.doesNotMatch(out, /FATAL/);
});
