import type { RequestContext } from "../../../utils/http.ts";

function queueApiBaseUrl(): string {
  return process.env.ASYNQ_API_BASE_URL || "http://localhost:8001";
}

async function proxy(req: Request, slug: string): Promise<Response> {
  const base = queueApiBaseUrl();
  let target = "";

  if (req.method === "POST" && (slug === "reload" || slug === "untagged" || slug === "reconcile")) {
    target = `${base}/api/autotag/${slug}`;
  } else if (req.method === "GET" && slug === "status") {
    target = `${base}/api/autotag/status`;
  } else if (req.method === "GET" && slug === "retag-status") {
    target = `${base}/api/autotag/retag-status`;
  } else {
    return new Response(null, { status: 404 });
  }

  const upstream = await fetch(target, {
    signal: AbortSignal.timeout(10_000),
    method: req.method,
    headers: { "Content-Type": "application/json" },
  });
  const body = await upstream.text();
  return new Response(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}


export const handler = async (req: Request, ctx: RequestContext): Promise<Response> => {
  const slug = ctx.params.slug;
  if (req.method !== "POST" && req.method !== "GET") {
    return new Response(null, { status: 405 });
  }

  try {
    return await proxy(req, slug);
  } catch (error) {
    console.error(`Failed to proxy /api/autotag/${slug}:`, error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
