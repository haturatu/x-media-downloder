import { FreshContext } from "$fresh/server.ts";

function queueApiBaseUrl(): string {
  return Deno.env.get("ASYNQ_API_BASE_URL") || "http://queue-api:8001";
}

async function proxy(req: Request, slug: string): Promise<Response> {
  const base = queueApiBaseUrl();
  let target = "";

  if (req.method === "POST" && (slug === "reload" || slug === "untagged" || slug === "reconcile")) {
    target = `${base}/api/autotag/${slug}`;
  } else if (req.method === "GET" && slug === "status") {
    target = `${base}/api/autotag/status`;
  } else {
    return new Response(null, { status: 404 });
  }

  const upstream = await fetch(target, {
    method: req.method,
    headers: { "Content-Type": "application/json" },
  });
  const body = await upstream.text();
  return new Response(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}


export const handler = async (req: Request, ctx: FreshContext): Promise<Response> => {
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
