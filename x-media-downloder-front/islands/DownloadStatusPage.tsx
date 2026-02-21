import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import { getApiBaseUrl } from "../utils/api.ts";

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

interface DownloadStatusResponse {
  queue_depth: number;
  summary: {
    total: number;
    pending: number;
    success: number;
    failure: number;
  };
  items: DownloadTaskStatus[];
}

export default function DownloadStatusPage() {
  const [status, setStatus] = useState<DownloadStatusResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  const fetchStatus = async () => {
    if (!IS_BROWSER) return;
    try {
      setError(null);
      const res = await fetch(`${API_BASE_URL}/api/download`);
      if (!res.ok) {
        throw new Error(`HTTP error! status: ${res.status}`);
      }
      const data: DownloadStatusResponse = await res.json();
      setStatus(data);
    } catch (err) {
      console.error("Error fetching download status:", err);
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchStatus();
    const interval = setInterval(fetchStatus, 2000);
    return () => clearInterval(interval);
  }, []);

  return (
    <>
      <Head>
        <title>Download Status - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <div class="status-head">
          <h2 class="page-title">Celery Download Status</h2>
          <button type="button" class="btn btn-secondary" onClick={fetchStatus}>
            Refresh
          </button>
        </div>

        {loading && <p>Loading status...</p>}
        {error && <p class="error-text">Error: {error}</p>}

        {status && (
          <>
            <div class="status-grid">
              <div class="status-tile">
                <span>Queue Depth</span>
                <strong>{status.queue_depth}</strong>
              </div>
              <div class="status-tile">
                <span>Tracked Tasks</span>
                <strong>{status.summary.total}</strong>
              </div>
              <div class="status-tile">
                <span>Running/Pending</span>
                <strong>{status.summary.pending}</strong>
              </div>
              <div class="status-tile">
                <span>Failed</span>
                <strong>{status.summary.failure}</strong>
              </div>
            </div>

            {status.items.length === 0
              ? <p class="info-text">No tracked download tasks yet.</p>
              : (
                <div class="task-list">
                  {status.items.map((item) => (
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
