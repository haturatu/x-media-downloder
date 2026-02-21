import { FreshContext } from "$fresh/server.ts";

function queueApiBaseUrl(): string {
  return Deno.env.get("ASYNQ_API_BASE_URL") || "http://queue-api:8001";
}

async function proxyToQueueApi(req: Request): Promise<Response> {
  const base = queueApiBaseUrl();
  const incoming = new URL(req.url);
  const target = `${base}/api/download${incoming.search}`;

  const response = await fetch(target, {
    method: req.method,
    headers: { "Content-Type": "application/json" },
    body: req.method === "POST" ? await req.text() : undefined,
  });
  const body = await response.text();
  return new Response(body, {
    status: response.status,
    headers: { "Content-Type": "application/json" },
  });
}

export const handler = async (
  req: Request,
  _ctx: FreshContext,
): Promise<Response> => {
  if (req.method !== "POST" && req.method !== "GET") {
    return new Response(null, { status: 405 });
  }

  try {
    return await proxyToQueueApi(req);
  } catch (error) {
    console.error("Failed to proxy /api/download:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
