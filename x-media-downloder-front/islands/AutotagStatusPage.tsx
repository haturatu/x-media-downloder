// x-media-downloder-front/islands/AutotagStatusPage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import { LiveStatusPayload, TaskStatus, subscribeStatus } from "../utils/status_ws.ts";

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

export default function AutotagStatusPage() {
  const [status, setStatus] = useState<TaskStatus | null>(null);
  const [retagStatus, setRetagStatus] = useState<TaskStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    setError(null);
    const unsubscribe = subscribeStatus((payload: LiveStatusPayload) => {
      setStatus(payload.autotag ?? null);
      setRetagStatus(payload.retag ?? null);
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

        {retagStatus && (
          <div class="status-card" style={{ marginTop: "16px" }}>
            <p>
              <strong>Bulk Retag State:</strong> {retagStatus.state}
            </p>
            <p>
              <strong>Details:</strong> {retagStatus.status}
            </p>
            <p>
              <strong>Progress:</strong> {progressTextFor(retagStatus)}
            </p>
            <div class="progress-track">
              <div
                class="progress-bar"
                style={{ width: `${progressPercentFor(retagStatus)}%` }}
              >
                {Math.round(progressPercentFor(retagStatus))}%
              </div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
