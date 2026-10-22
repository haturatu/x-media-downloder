import type { RequestContext } from "../../../utils/http.ts";

function queueApiBaseUrl(): string {
  return process.env.ASYNQ_API_BASE_URL || "http://localhost:8001";
}

export const handler = async (
  req: Request,
  _ctx: RequestContext,
): Promise<Response> => {
  if (req.method !== "GET") {
    return new Response(null, { status: 405 });
  }

  try {
    const url = new URL(req.url);
    const target = `${queueApiBaseUrl()}/api/tasks/status${url.search}`;
    const upstream = await fetch(target, {
    signal: AbortSignal.timeout(10_000),
      method: "GET",
      headers: { "Content-Type": "application/json" },
    });
    const body = await upstream.text();
    return new Response(body, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  } catch (error) {
    console.error("Error proxying task status API:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
