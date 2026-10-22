import express, { type ErrorRequestHandler } from "express";
import { createServer } from "node:http";
import { readFile, realpath } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { WebSocketServer, WebSocket } from "ws";
import { buildPayload } from "./routes/api/ws/status.ts";

export async function startServer() {
  const production = process.argv.includes("--production");
  if (production) process.env.NODE_ENV = "production";
  const root = process.cwd();
  const app = express();
  app.disable("x-powered-by");
  const server = createServer(app);
  const vite = production ? undefined : await (await import("vite")).createServer({
    server: { middlewareMode: true, hmr: { server } },
    appType: "custom",
  });
  const clientDir = path.resolve(root, "dist/client");
  const template = production ? await readFile(path.join(clientDir, "index.html"), "utf8") : "";
  const renderer = production ? await import("./entry-server.tsx") : undefined;
  const mediaRoot = path.resolve(process.env.MEDIA_ROOT || "../downloaded_images");

  app.use("/images", async (req, res, next) => {
    if (req.method !== "GET" && req.method !== "HEAD") {
      res.set("Allow", "GET, HEAD").status(405).end();
      return;
    }
    try {
      const filename = decodeURIComponent(req.path).replace(/^\/+/, "");
      if (!filename || filename.includes("\0") || filename.includes("\\") || filename.split("/").includes("..")) {
        res.status(400).send("Invalid path");
        return;
      }
      // Resolve symlinks too, so files outside MEDIA_ROOT cannot be served.
      const [resolvedRoot, resolvedFile] = await Promise.all([
        realpath(mediaRoot), realpath(path.resolve(mediaRoot, filename)),
      ]);
      const relative = path.relative(resolvedRoot, resolvedFile);
      if (!relative || relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
        res.status(400).send("Invalid path");
        return;
      }
      res.sendFile(resolvedFile, { maxAge: "1h", dotfiles: "deny" }, (error) => {
        if (error) next(error);
      });
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ENOENT" || (error as NodeJS.ErrnoException).code === "ENOTDIR") {
        res.status(404).send("Image not found");
      } else if (error instanceof URIError) {
        res.status(400).send("Invalid path");
      } else next(error);
    }
  });

  if (vite) app.use(vite.middlewares);
  else {
    app.use("/assets", express.static(path.join(clientDir, "assets"), { immutable: true, maxAge: "1y" }));
    app.use(express.static(clientDir, { index: false, maxAge: "1h" }));
  }

  app.use(express.raw({ type: () => true, limit: "10mb" }));
  app.use(async (req, res) => {
    // Use a fixed internal origin; no trust in the incoming Host header is needed.
    const url = new URL(req.originalUrl, "http://localhost");
    const request = new Request(url, {
      method: req.method,
      headers: req.headers as HeadersInit,
      body: req.method === "GET" || req.method === "HEAD" ? undefined : req.body,
    });
    const currentTemplate = vite
      ? await vite.transformIndexHtml(req.originalUrl, await readFile(path.join(root, "index.html"), "utf8"))
      : template;
    const currentRenderer = vite ? await vite.ssrLoadModule("/entry-server.tsx") : renderer!;
    const response: Response = await currentRenderer.handleRequest(request, currentTemplate);
    res.status(response.status);
    response.headers.forEach((value, key) => res.setHeader(key, value));
    res.end(req.method === "HEAD" ? undefined : Buffer.from(await response.arrayBuffer()));
  });

  const onError: ErrorRequestHandler = (error, _req, res, _next) => {
    const status = error instanceof URIError ? 400 : Number(error.status) || 500;
    if (status >= 500) console.error("ERROR Request failed:", error);
    if (!res.headersSent) res.status(status).send(status >= 500 ? "Internal Server Error" : "Invalid request");
    else res.destroy();
  };
  app.use(onError);

  const sockets = new WebSocketServer({ noServer: true, maxPayload: 1024 });
  server.on("upgrade", (req, socket, head) => {
    if (new URL(req.url || "/", "http://localhost").pathname !== "/api/ws/status") {
      // Vite handles its own HMR upgrades in development.
      if (production) socket.destroy();
      return;
    }
    sockets.handleUpgrade(req, socket, head, (ws) => sockets.emit("connection", ws));
  });
  sockets.on("connection", (socket) => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = () => { controller.abort(); clearTimeout(timer); };
    socket.on("close", stop);
    socket.on("error", (error) => { console.error("ERROR Status WebSocket:", error); stop(); });
    const push = async () => {
      try {
        const payload = await buildPayload(controller.signal);
        if (socket.readyState !== WebSocket.OPEN) return;
        socket.send(JSON.stringify(payload));
        timer = setTimeout(push, 2000);
      } catch (error) {
        console.error("ERROR Sending status:", error);
        socket.close(1011);
      }
    };
    void push();
  });

  const port = Number(process.env.PORT || 8000);
  if (!Number.isInteger(port) || port < 0 || port > 65535) throw new Error("Invalid PORT");
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, process.env.HOST || "0.0.0.0", () => {
      server.off("error", reject);
      resolve();
    });
  });
  server.on("error", (error) => { console.error("FATAL HTTP server:", error); process.exit(1); });
  console.log(`INFO Frontend listening on port ${(server.address() as import("node:net").AddressInfo).port}`);
  let stopping = false;
  const shutdown = () => {
    if (stopping) return;
    stopping = true;
    console.log("INFO Shutting down frontend");
    const deadline = setTimeout(() => { console.error("FATAL Shutdown timed out"); process.exit(1); }, 10_000);
    deadline.unref();
    for (const socket of sockets.clients) socket.terminate();
    sockets.close();
    server.close(() => {
      void vite?.close().finally(() => clearTimeout(deadline));
      if (!vite) clearTimeout(deadline);
    });
  };
  process.once("SIGTERM", shutdown);
  process.once("SIGINT", shutdown);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  startServer().catch((error) => { console.error("FATAL Failed to start frontend:", error); process.exit(1); });
}
