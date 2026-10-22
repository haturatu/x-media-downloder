import { renderToString } from "preact-render-to-string";
import type { ComponentChildren } from "preact";
import { App, type AppState, type PageState } from "./app.tsx";
import { HeadContext } from "./utils/runtime.tsx";
import type { Handler } from "./utils/http.ts";
import { handler as home } from "./routes/index.tsx";
import { handler as users } from "./routes/users.tsx";
import { handler as userTweets } from "./routes/users/[username].tsx";
import { handler as tags } from "./routes/tags.tsx";
import { handler as tagImages } from "./routes/tags/[tag].tsx";
import { handler as download } from "./routes/api/download.ts";
import { handler as images } from "./routes/api/images.ts";
import { handler as bulkDelete } from "./routes/api/images/bulk-delete.ts";
import { handler as retag } from "./routes/api/images/retag.ts";
import { handler as retagBulk } from "./routes/api/images/retag-bulk.ts";
import { handler as apiUsers } from "./routes/api/users.ts";
import { handler as apiTweets } from "./routes/api/users/[username]/tweets.ts";
import { handler as apiTags } from "./routes/api/tags.ts";
import { handler as tasks } from "./routes/api/tasks/status.ts";
import { handler as autotag } from "./routes/api/autotag/[...slug].ts";

interface PageRoute {
  pattern: URLPattern;
  run(request: Request, params: Record<string, string>, template: string): Promise<Response>;
}

export function renderPage(template: string, state: AppState, status = 200): Response {
  const head: ComponentChildren[] = [];
  const html = renderToString(
    <HeadContext.Provider value={head}><App {...state} /></HeadContext.Provider>,
  );
  // JSON is embedded in a script element: escape delimiters even for application/json.
  const data = JSON.stringify(state).replace(/</g, "\\u003c").replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
  return new Response(template
    .replace("<!--app-head-->", () => renderToString(<>{head}</>))
    .replace("<!--app-html-->", () => html)
    .replace("<!--app-data-->", () => data), {
      status,
      headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" },
    });
}

function pageRoute<T>(path: string, handler: Handler<T>, page: PageState["page"]): PageRoute {
  return {
    pattern: new URLPattern({ pathname: path }),
    async run(request, params, template) {
      return await handler(request, {
        params,
        render: (data) => renderPage(template, { page, data, route: new URL(request.url).pathname, search: new URL(request.url).search } as AppState),
      });
    },
  };
}

const pages = [
  pageRoute("/", home, "home"),
  pageRoute("/users", users, "users"),
  pageRoute("/users/:username", userTweets, "userTweets"),
  pageRoute("/tags", tags, "tags"),
  pageRoute("/tags/:tag", tagImages, "tagImages"),
];

const apiDefinitions: [string, Handler][] = [
  ["/api/download", download],
  ["/api/images", images],
  ["/api/images/bulk-delete", bulkDelete],
  ["/api/images/retag", retag],
  ["/api/images/retag-bulk", retagBulk],
  ["/api/users", apiUsers],
  ["/api/users/:username/tweets", apiTweets],
  ["/api/tags", apiTags],
  ["/api/tasks/status", tasks],
  ["/api/autotag/:slug*", autotag],
];
const apiRoutes = apiDefinitions.map(([pathname, handler]) => [new URLPattern({ pathname }), handler] as const);

function decodedParams(groups: Record<string, string | undefined>): Record<string, string> {
  return Object.fromEntries(Object.entries(groups).map(([key, value]) => [key, decodeURIComponent(value ?? "")]));
}

export async function handleRequest(request: Request, template: string): Promise<Response> {
  const url = new URL(request.url);
  const pathname = url.pathname.replace(/\/$/, "") || "/";
  url.pathname = pathname;
  if (pathname === "/api/ws/status") return new Response("WebSocket upgrade required", { status: 426 });
  for (const [pattern, handler] of apiRoutes) {
    const match = pattern.exec(url);
    if (match) return await handler(request, {
      params: decodedParams(match.pathname.groups),
      render: () => { throw new Error("API routes cannot render pages"); },
    });
  }
  if (pathname.startsWith("/api/")) return Response.json({ error: "Not Found" }, { status: 404 });
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("Method Not Allowed", { status: 405, headers: { Allow: "GET, HEAD" } });
  }
  for (const page of pages) {
    const match = page.pattern.exec(url);
    if (match) return await page.run(request, decodedParams(match.pathname.groups), template);
  }
  const page = pathname === "/download-status" ? "downloadStatus"
    : pathname === "/autotag-status" ? "autotagStatus" : "notFound";
  return renderPage(template, { page, data: null, route: pathname, search: url.search }, page === "notFound" ? 404 : 200);
}
