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

export default function AutotagStatusPage() {
  const [status, setStatus] = useState<AutotagStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  const fetchStatus = async () => {
    if (!IS_BROWSER) return;
    try {
      // Don't set loading to true on polls
      // setLoading(true); 
      setError(null);
      const res = await fetch(`${API_BASE_URL}/api/autotag/status`);
      if (!res.ok) {
        throw new Error(`HTTP error! status: ${res.status}`);
      }
      const data: AutotagStatus = await res.json();
      setStatus(data);
    } catch (err) {
      console.error("Error fetching autotag status:", err);
      setError(err.message);
    } finally {
      setLoading(false); // Only set loading to false on first load
    }
  };

  useEffect(() => {
    fetchStatus(); // Initial fetch

    const interval = setInterval(() => {
      if (status && (status.state === "SUCCESS" || status.state === "FAILURE" || status.state === "NOT_FOUND")) {
        clearInterval(interval); // Stop polling if task is complete or failed
        return;
      }
      fetchStatus();
    }, 2000); // Poll every 2 seconds

    return () => clearInterval(interval); // Cleanup on unmount
  }, [status]); // Re-run effect if status changes to check for completion

  let progressPercent = 0;
  if (status && status.total && status.total > 0 && status.current !== undefined) {
    progressPercent = (status.current / status.total) * 100;
  } else if (status?.state === "SUCCESS") {
    progressPercent = 100;
  }

  const progressText = (status?.state === "PROGRESS" || status?.state === "SUCCESS")
    ? `${status.current} / ${status.total}`
    : "N/A";

  return (
    <>
      <Head>
        <title>Autotagger Status - X Media Downloader</title>
      </Head>
      <div class="p-4">
        <h2 class="text-2xl font-bold mb-4">Autotagging Task Status</h2>
        {loading && <p>Loading status...</p>}
        {error && <p class="text-red-500">Error: {error}</p>}

        {status && (
          <div class="bg-gray-800 p-6 rounded-lg shadow-lg">
            <p class="text-lg mb-2"><strong>State:</strong> {status.state}</p>
            <p class="text-md mb-4"><strong>Details:</strong> {status.status}</p>

            <p class="text-sm"><strong>Progress:</strong> {progressText}</p>
            <div class="w-full bg-gray-700 rounded-full h-6 mb-4">
              <div
                class="bg-blue-600 h-6 rounded-full text-center text-white text-sm leading-6 transition-all duration-500 ease-out"
                style={{ width: `${progressPercent}%` }}
              >
                {Math.round(progressPercent)}%
              </div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
