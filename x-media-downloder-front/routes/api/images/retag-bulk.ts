import type { RequestContext } from "../../../utils/http.ts";

function queueApiBaseUrl(): string {
  return process.env.ASYNQ_API_BASE_URL || "http://localhost:8001";
}

export const handler = async (
  req: Request,
  _ctx: RequestContext,
): Promise<Response> => {
  if (req.method !== "POST") {
    return new Response(null, { status: 405 });
  }

  try {
    const upstream = await fetch(`${queueApiBaseUrl()}/api/images/retag/bulk`, {
      signal: AbortSignal.timeout(10_000),
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
    console.error("Error proxying bulk image retag API:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
