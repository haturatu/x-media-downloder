import { useEffect, useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import { getApiBaseUrl } from "../utils/api.ts";
import { LiveStatusPayload, subscribeStatus } from "../utils/status_ws.ts";

interface AutotagMenuModalProps {
  isOpen: boolean;
  onClose: () => void;
  onShowStatus: () => void; // Callback to navigate to status page
}

export default function AutotagMenuModal(
  { isOpen, onClose, onShowStatus }: AutotagMenuModalProps,
) {
  const [forceRetagConfirm, setForceRetagConfirm] = useState(false);
  const [tagUntaggedLoading, setTagUntaggedLoading] = useState(false);
  const [forceRetagLoading, setForceRetagLoading] = useState(false);
  const [reconcileLoading, setReconcileLoading] = useState(false);
  const [statusMessage, setStatusMessage] = useState<string | null>(null);
  const [autotagBusy, setAutotagBusy] = useState(false);

  const API_BASE_URL = getApiBaseUrl();

  // Reset state when modal is closed
  useEffect(() => {
    if (!isOpen) {
      setForceRetagConfirm(false);
      setTagUntaggedLoading(false);
      setForceRetagLoading(false);
      setReconcileLoading(false);
      setStatusMessage(null);
    }
  }, [isOpen]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const unsubscribe = subscribeStatus((payload: LiveStatusPayload) => {
      setAutotagBusy(Boolean(payload.locks?.autotagBusy));
    });
    return () => unsubscribe();
  }, []);

  const handleTagUntagged = async () => {
    if (!IS_BROWSER) return;
    if (autotagBusy) {
      setStatusMessage("Another autotag task is already running.");
      return;
    }
    setTagUntaggedLoading(true);
    setStatusMessage("Starting to tag untagged images in the background...");
    try {
      const res = await fetch(`${API_BASE_URL}/api/autotag/untagged`, {
        method: "POST",
      });
      const data = await res.json();
      if (data.success) {
        setStatusMessage(data.message);
        onShowStatus(); // Navigate to status page on success
      } else {
        setStatusMessage(
          `Error: ${data.message || "Failed to start tag untagged task."}`,
        );
      }
    } catch (error) {
      console.error("Error tagging untagged images:", error);
      setStatusMessage(`Error: ${error.message}`);
    } finally {
      // Don't set loading to false here, as we are navigating away
    }
  };

  const handleForceRetag = async () => {
    if (!IS_BROWSER || !forceRetagConfirm) return;
    if (autotagBusy) {
      setStatusMessage("Another autotag task is already running.");
      return;
    }
    setForceRetagLoading(true);
    setStatusMessage(
      "Starting to force re-tag all images. This will take a while...",
    );
    try {
      const res = await fetch(`${API_BASE_URL}/api/autotag/reload`, {
        method: "POST",
      });
      const data = await res.json();
      if (data.success) {
        setStatusMessage(data.message);
        onShowStatus(); // Navigate to status page on success
      } else {
        setStatusMessage(
          `Error: ${data.message || "Failed to start force re-tag task."}`,
        );
      }
    } catch (error) {
      console.error("Error force re-tagging images:", error);
      setStatusMessage(`Error: ${error.message}`);
    } finally {
      // Don't set loading to false here
    }
  };

  const handleReconcile = async () => {
    if (!IS_BROWSER) return;
    if (autotagBusy) {
      setStatusMessage("Another autotag task is already running.");
      return;
    }
    setReconcileLoading(true);
    setStatusMessage(
      "Starting consistency reconciliation (file system vs DB hashes)...",
    );
    try {
      const res = await fetch(`${API_BASE_URL}/api/autotag/reconcile`, {
        method: "POST",
      });
      const data = await res.json();
      if (data.success) {
        setStatusMessage(data.message);
        onShowStatus();
      } else {
        setStatusMessage(
          `Error: ${data.message || "Failed to start reconciliation task."}`,
        );
      }
    } catch (error) {
      console.error("Error starting reconciliation task:", error);
      setStatusMessage(`Error: ${error.message}`);
    } finally {
      // Don't set loading to false here
    }
  };

  return (
    <div
      class={`autotag-menu-modal ${isOpen ? "visible" : ""}`}
      onClick={onClose}
    >
      <div class="autotag-menu-content" onClick={(e) => e.stopPropagation()}>
        <div class="autotag-menu-header">
          <h3>Autotagger Options</h3>
          <span class="autotag-menu-close" onClick={onClose}>&times;</span>
        </div>
        <div class="autotag-menu-body">
          <button type="button" class="header-btn" onClick={onShowStatus}>
            View Task Status
          </button>
          <hr />
          <button
            type="button"
            class="btn btn-success"
            onClick={handleTagUntagged}
            disabled={autotagBusy || tagUntaggedLoading || forceRetagLoading || reconcileLoading}
          >
            {tagUntaggedLoading ? "Processing..." : "Tag Untagged Images"}
          </button>
          <button
            type="button"
            class="btn"
            onClick={handleReconcile}
            disabled={autotagBusy || tagUntaggedLoading || forceRetagLoading || reconcileLoading}
          >
            {reconcileLoading ? "Processing..." : "Reconcile DB With Files"}
          </button>
          <hr />
          <div class="force-retag-section">
            <p>
              <strong>Danger Zone:</strong>{" "}
              This will delete all tags and re-tag every image.
            </p>
            <label>
              <input
                type="checkbox"
                checked={forceRetagConfirm}
                onInput={(e) => setForceRetagConfirm(e.currentTarget.checked)}
              />
              I understand this is slow and irreversible.
            </label>
            <button
              type="button"
              class="btn btn-danger"
              onClick={handleForceRetag}
              disabled={!forceRetagConfirm || forceRetagLoading ||
                tagUntaggedLoading || reconcileLoading || autotagBusy}
            >
              {forceRetagLoading ? "Processing..." : "Force Re-tag All Images"}
            </button>
          </div>
          {statusMessage && (
            <p class="muted-message centered">{statusMessage}</p>
          )}
        </div>
      </div>
    </div>
  );
}
