// x-media-downloder-front/islands/AutotagStatusPage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import { getApiBaseUrl } from "../utils/api.ts";

interface AutotagStatus {
  state: string;
  status: string;
  current?: number;
  total?: number;
}

function isTerminalState(state?: string): boolean {
  return state === "SUCCESS" || state === "FAILURE" || state === "NOT_FOUND";
}

function progressPercentFor(status: AutotagStatus | null): number {
  if (!status) return 0;
  if (status.total && status.total > 0 && status.current !== undefined) {
    return (status.current / status.total) * 100;
  }
  if (status.state === "SUCCESS") {
    return 100;
  }
  return 0;
}

function progressTextFor(status: AutotagStatus | null): string {
  if (!status) return "N/A";
  if (status.state === "PROGRESS" || status.state === "SUCCESS") {
    return `${status.current ?? 0} / ${status.total ?? 0}`;
  }
  return "N/A";
}

export default function AutotagStatusPage() {
  const [status, setStatus] = useState<AutotagStatus | null>(null);
  const [retagStatus, setRetagStatus] = useState<AutotagStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  const fetchStatus = async () => {
    if (!IS_BROWSER) return;
    try {
      setError(null);
      const [autotagRes, retagRes] = await Promise.all([
        fetch(`${API_BASE_URL}/api/autotag/status`),
        fetch(`${API_BASE_URL}/api/autotag/retag-status`),
      ]);
      if (!autotagRes.ok) {
        throw new Error(`Autotag status HTTP error: ${autotagRes.status}`);
      }
      if (!retagRes.ok) {
        throw new Error(`Retag status HTTP error: ${retagRes.status}`);
      }
      const [autotagData, retagData] = await Promise.all([
        autotagRes.json() as Promise<AutotagStatus>,
        retagRes.json() as Promise<AutotagStatus>,
      ]);
      setStatus(autotagData);
      setRetagStatus(retagData);
    } catch (err) {
      console.error("Error fetching autotag status:", err);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchStatus();

    const interval = setInterval(() => {
      const autotagDone = isTerminalState(status?.state);
      const retagDone = isTerminalState(retagStatus?.state);
      if (autotagDone && retagDone) {
        clearInterval(interval);
        return;
      }
      fetchStatus();
    }, 2000);

    return () => clearInterval(interval);
  }, [status, retagStatus]);

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
              <strong>Progress:</strong> {progressText}
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
