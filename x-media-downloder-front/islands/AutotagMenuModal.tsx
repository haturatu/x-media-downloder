import { useState, useEffect } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import { getApiBaseUrl } from "../utils/api.ts";

interface AutotagMenuModalProps {
  isOpen: boolean;
  onClose: () => void;
  onShowStatus: () => void; // Callback to navigate to status page
}

export default function AutotagMenuModal({ isOpen, onClose, onShowStatus }: AutotagMenuModalProps) {
  const [forceRetagConfirm, setForceRetagConfirm] = useState(false);
  const [tagUntaggedLoading, setTagUntaggedLoading] = useState(false);
  const [forceRetagLoading, setForceRetagLoading] = useState(false);
  const [statusMessage, setStatusMessage] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  // Reset state when modal is closed
  useEffect(() => {
    if (!isOpen) {
      setForceRetagConfirm(false);
      setTagUntaggedLoading(false);
      setForceRetagLoading(false);
      setStatusMessage(null);
    }
  }, [isOpen]);

  const handleTagUntagged = async () => {
    if (!IS_BROWSER) return;
    setTagUntaggedLoading(true);
    setStatusMessage("Starting to tag untagged images in the background...");
    try {
      const res = await fetch(`${API_BASE_URL}/api/autotag/untagged`, { method: "POST" });
      const data = await res.json();
      if (data.success) {
        setStatusMessage(data.message);
        onShowStatus(); // Navigate to status page on success
      } else {
        setStatusMessage(`Error: ${data.message || "Failed to start tag untagged task."}`);
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
    setForceRetagLoading(true);
    setStatusMessage("Starting to force re-tag all images. This will take a while...");
    try {
      const res = await fetch(`${API_BASE_URL}/api/autotag/reload`, { method: "POST" });
      const data = await res.json();
      if (data.success) {
        setStatusMessage(data.message);
        onShowStatus(); // Navigate to status page on success
      } else {
        setStatusMessage(`Error: ${data.message || "Failed to start force re-tag task."}`);
      }
    } catch (error) {
      console.error("Error force re-tagging images:", error);
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
          <button class="header-btn" onClick={onShowStatus}>View Task Status</button>
          <hr />
          <button
            class="header-btn"
            style={{backgroundColor: '#28a745', borderColor: '#28a745'}}
            onClick={handleTagUntagged}
            disabled={tagUntaggedLoading || forceRetagLoading}
          >
            {tagUntaggedLoading ? "Processing..." : "Tag Untagged Images"}
          </button>
          <hr />
          <div class="force-retag-section">
            <p><strong>Danger Zone:</strong> This will delete all tags and re-tag every image.</p>
            <label>
              <input
                type="checkbox"
                checked={forceRetagConfirm}
                onInput={(e) => setForceRetagConfirm(e.currentTarget.checked)}
              />
              I understand this is slow and irreversible.
            </label>
            <button
              class="header-btn"
              style={{backgroundColor: '#dc3545', borderColor: '#dc3545'}}
              onClick={handleForceRetag}
              disabled={!forceRetagConfirm || forceRetagLoading || tagUntaggedLoading}
            >
              {forceRetagLoading ? "Processing..." : "Force Re-tag All Images"}
            </button>
          </div>
          {statusMessage && <p style={{fontSize: '0.8rem', color: '#888', marginTop: '1rem', textAlign: 'center'}}>{statusMessage}</p>}
        </div>
      </div>
    </div>
  );
}
