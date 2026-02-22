// x-media-downloder-front/islands/AutotagStatusPage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import {
  LiveStatusPayload,
  subscribeStatus,
  TaskStatus,
} from "../utils/status_ws.ts";

function progressPercentFor(status: TaskStatus | null): number {
  if (!status) return 0;
  if (status.total && status.total > 0 && status.current !== undefined) {
    return (status.current / status.total) * 100;
  }
  if (status.state === "SUCCESS") {
    return 100;
  }
  return 0;
}

function progressTextFor(status: TaskStatus | null): string {
  if (!status) return "N/A";
  if (status.state === "PROGRESS" || status.state === "SUCCESS") {
    return `${status.current ?? 0} / ${status.total ?? 0}`;
  }
  return "N/A";
}

function statePriority(state?: string): number {
  switch (state) {
    case "PROGRESS":
      return 4;
    case "PENDING":
      return 3;
    case "SUCCESS":
    case "FAILURE":
      return 2;
    case "NOT_FOUND":
    default:
      return 1;
  }
}

function pickUnifiedStatus(
  autotag: TaskStatus | null,
  retag: TaskStatus | null,
): { status: TaskStatus | null; source: "autotag" | "retag" | null } {
  const a = autotag ?? null;
  const r = retag ?? null;
  if (!a && !r) return { status: null, source: null };
  if (!a) return { status: r, source: "retag" };
  if (!r) return { status: a, source: "autotag" };
  if (statePriority(r.state) > statePriority(a.state)) {
    return { status: r, source: "retag" };
  }
  return { status: a, source: "autotag" };
}

export default function AutotagStatusPage() {
  const [status, setStatus] = useState<TaskStatus | null>(null);
  const [statusSource, setStatusSource] = useState<"autotag" | "retag" | null>(
    null,
  );
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    setError(null);
    const unsubscribe = subscribeStatus((payload: LiveStatusPayload) => {
      const unified = pickUnifiedStatus(
        payload.autotag ?? null,
        payload.retag ?? null,
      );
      setStatus(unified.status);
      setStatusSource(unified.source);
      setLoading(false);
    });
    return () => unsubscribe();
  }, []);

  return (
    <>
      <Head>
        <title>Autotagger Status - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <h2 class="page-title">Autotagging Task Status</h2>
        {loading && <p>Loading status...</p>}
        {error && <p class="error-text">Error: {error}</p>}

        {status && (
          <div class="status-card">
            <p>
              <strong>Type:</strong>{" "}
              {statusSource === "retag" ? "Bulk Retag" : "Autotag"}
            </p>
            <p>
              <strong>State:</strong> {status.state}
            </p>
            <p>
              <strong>Details:</strong> {status.status}
            </p>

            <p>
              <strong>Progress:</strong> {progressTextFor(status)}
            </p>
            <div class="progress-track">
              <div
                class="progress-bar"
                style={{ width: `${progressPercentFor(status)}%` }}
              >
                {Math.round(progressPercentFor(status))}%
              </div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
