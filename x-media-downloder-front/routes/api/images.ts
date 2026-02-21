import { FreshContext } from "$fresh/server.ts";

function queueApiBaseUrl(): string {
  return Deno.env.get("ASYNQ_API_BASE_URL") || "http://queue-api:8001";
}

export const handler = async (
  req: Request,
  _ctx: FreshContext,
): Promise<Response> => {
  if (req.method !== "GET" && req.method !== "DELETE") {
    return new Response(null, { status: 405 });
  }

  try {
    const url = new URL(req.url);
    const target = `${queueApiBaseUrl()}/api/images${url.search}`;
    const upstream = await fetch(target, {
      method: req.method,
      headers: { "Content-Type": "application/json" },
      body: req.method === "DELETE" ? await req.text() : undefined,
    });
    const body = await upstream.text();
    return new Response(body, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (error) {
    console.error("Error proxying images API:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
