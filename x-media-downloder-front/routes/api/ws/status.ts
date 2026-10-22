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
  return process.env.ASYNQ_API_BASE_URL || "http://localhost:8001";
}

function isBusyState(state?: string): boolean {
  return state === "PENDING" || state === "PROGRESS";
}

async function fetchJSON<T>(url: string, signal: AbortSignal): Promise<T | null> {
  try {
    const res = await fetch(url, { signal: AbortSignal.any([signal, AbortSignal.timeout(10_000)]) });
    if (!res.ok) return null;
    return await res.json() as T;
  } catch {
    return null;
  }
}

export async function buildPayload(signal: AbortSignal) {
  const base = queueApiBaseUrl();
  const [autotag, retag, download] = await Promise.all([
    fetchJSON<TaskStatusLike>(`${base}/api/autotag/status`, signal),
    fetchJSON<TaskStatusLike>(`${base}/api/autotag/retag-status`, signal),
    fetchJSON<DownloadStatusLike>(`${base}/api/download`, signal),
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
