import { FreshContext } from "$fresh/server.ts";

interface TaskStatusLike {
  state?: string;
  status?: string;
  message?: string;
  current?: number;
  total?: number;
}

interface DownloadStatusLike {
  queue_depth?: number;
  summary?: {
    total?: number;
    pending?: number;
    success?: number;
    failure?: number;
  };
  items?: unknown[];
}

function queueApiBaseUrl(): string {
  return Deno.env.get("ASYNQ_API_BASE_URL") || "http://queue-api:8001";
}

function isBusyState(state?: string): boolean {
  return state === "PENDING" || state === "PROGRESS";
}

async function fetchJSON<T>(url: string): Promise<T | null> {
  try {
    const res = await fetch(url);
    if (!res.ok) return null;
    return await res.json() as T;
  } catch {
    return null;
  }
}

async function buildPayload() {
  const base = queueApiBaseUrl();
  const [autotag, retag, download] = await Promise.all([
    fetchJSON<TaskStatusLike>(`${base}/api/autotag/status`),
    fetchJSON<TaskStatusLike>(`${base}/api/autotag/retag-status`),
    fetchJSON<DownloadStatusLike>(`${base}/api/download`),
  ]);

  return {
    type: "status",
    ts: Date.now(),
    autotag: autotag ?? { state: "NOT_FOUND", status: "Unavailable" },
    retag: retag ?? { state: "NOT_FOUND", status: "Unavailable" },
    download: download ?? { queue_depth: 0, summary: { total: 0, pending: 0, success: 0, failure: 0 }, items: [] },
    locks: {
      autotagBusy: isBusyState(autotag?.state),
      retagBusy: isBusyState(retag?.state),
    },
  };
}

export const handler = (req: Request, _ctx: FreshContext): Response => {
  const { socket, response } = Deno.upgradeWebSocket(req);
  let timer: number | null = null;

  const push = async () => {
    if (socket.readyState !== WebSocket.OPEN) return;
    const payload = await buildPayload();
    socket.send(JSON.stringify(payload));
  };

  socket.onopen = () => {
    push();
    timer = setInterval(push, 2000) as unknown as number;
  };

  socket.onerror = () => {
    if (timer !== null) {
      clearInterval(timer);
      timer = null;
    }
  };

  socket.onclose = () => {
    if (timer !== null) {
      clearInterval(timer);
      timer = null;
    }
  };

  return response;
};
