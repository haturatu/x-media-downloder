import { FreshContext } from "$fresh/server.ts";

function queueApiBaseUrl(): string {
  return Deno.env.get("ASYNQ_API_BASE_URL") || "http://queue-api:8001";
}

export const handler = async (
  req: Request,
  ctx: FreshContext<unknown, { username: string }>,
): Promise<Response> => {
  if (req.method !== "GET") {
    return new Response(null, { status: 405 });
  }

  try {
    const url = new URL(req.url);
    const target = `${queueApiBaseUrl()}/api/users/${encodeURIComponent(ctx.params.username)}/tweets${url.search}`;
    const upstream = await fetch(target, {
      method: "GET",
      headers: { "Content-Type": "application/json" },
    });
    const body = await upstream.text();
    return new Response(body, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (error) {
    console.error("Error proxying user tweets API:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
