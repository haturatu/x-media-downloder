import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import { DownloadStatus, LiveStatusPayload, subscribeStatus } from "../utils/status_ws.ts";

interface DownloadTaskStatus {
  task_id: string;
  url: string | null;
  state: "PENDING" | "PROGRESS" | "SUCCESS" | "FAILURE";
  message: string;
  current?: number;
  total?: number;
  downloaded_count?: number;
  skipped_count?: number;
}

type DownloadStatusResponse = DownloadStatus;

export default function DownloadStatusPage() {
  const [status, setStatus] = useState<DownloadStatusResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    setError(null);
    const unsubscribe = subscribeStatus((payload: LiveStatusPayload) => {
      setStatus(payload.download ?? null);
      setLoading(false);
    });
    return () => unsubscribe();
  }, []);

  return (
    <>
      <Head>
        <title>Download Status - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <div class="status-head">
          <h2 class="page-title">Asynq Download Status</h2>
          <span class="info-text">Live via WebSocket</span>
        </div>

        {loading && <p>Loading status...</p>}
        {error && <p class="error-text">Error: {error}</p>}

        {status && (
          <>
            <div class="status-grid">
              <div class="status-tile">
                <span>Queue Depth</span>
                <strong>{status.queue_depth ?? 0}</strong>
              </div>
              <div class="status-tile">
                <span>Tracked Tasks</span>
                <strong>{status.summary?.total ?? 0}</strong>
              </div>
              <div class="status-tile">
                <span>Running/Pending</span>
                <strong>{status.summary?.pending ?? 0}</strong>
              </div>
              <div class="status-tile">
                <span>Failed</span>
                <strong>{status.summary?.failure ?? 0}</strong>
              </div>
            </div>

            {(status.items?.length ?? 0) === 0
              ? <p class="info-text">No tracked download tasks yet.</p>
              : (
                <div class="task-list">
                  {(status.items as DownloadTaskStatus[]).map((item) => (
                    <article key={item.task_id} class="task-card">
                      <div class="task-row">
                        <span class={`task-state ${item.state.toLowerCase()}`}>
                          {item.state}
                        </span>
                        <code class="task-id">{item.task_id}</code>
                      </div>
                      <p class="task-url">{item.url || "Unknown URL"}</p>
                      <p class="task-message">{item.message}</p>
                      {(item.current !== undefined &&
                        item.total !== undefined) && (
                        <p class="task-counts">
                          progress: {item.current} / {item.total}
                        </p>
                      )}
                      {(item.downloaded_count !== undefined ||
                        item.skipped_count !== undefined) && (
                        <p class="task-counts">
                          downloaded: {item.downloaded_count ?? 0} / skipped:
                          {" "}
                          {item.skipped_count ?? 0}
                        </p>
                      )}
                    </article>
                  ))}
                </div>
              )}
          </>
        )}
      </div>
    </>
  );
}
