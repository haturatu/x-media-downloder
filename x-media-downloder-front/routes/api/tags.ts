import { FreshContext } from "$fresh/server.ts";
function queueApiBaseUrl(): string {
  return Deno.env.get("ASYNQ_API_BASE_URL") || "http://queue-api:8001";
}

export const handler = async (req: Request, _ctx: FreshContext): Promise<Response> => {
  if (req.method !== "GET") {
    return new Response(null, { status: 405 });
  }

  try {
    const url = new URL(req.url);
    const upstream = await fetch(`${queueApiBaseUrl()}/api/tags${url.search}`, {
      method: "GET",
      headers: { "Content-Type": "application/json" },
    });
    const body = await upstream.text();
    return new Response(body, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (error) {
    console.error("Error proxying tags:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
