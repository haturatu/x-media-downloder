import { FreshContext } from "$fresh/server.ts";

function queueApiBaseUrl(): string {
  return Deno.env.get("ASYNQ_API_BASE_URL") || "http://queue-api:8001";
}

export const handler = async (
  req: Request,
  _ctx: FreshContext,
): Promise<Response> => {
  if (req.method !== "POST") {
    return new Response(null, { status: 405 });
  }

  try {
    const upstream = await fetch(`${queueApiBaseUrl()}/api/images/bulk-delete`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: await req.text(),
    });
    const body = await upstream.text();
    return new Response(body, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (error) {
    console.error("Error proxying bulk delete images API:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
