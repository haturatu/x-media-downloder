import { useEffect, useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import type { Image } from "../utils/types.ts";
import { getApiBaseUrl } from "../utils/api.ts";

interface ImageModalProps {
  isOpen: boolean;
  onClose: () => void;
  initialImage: Image | null;
  allImages: Image[]; // All images in the current gallery for navigation
  onImageUpdate: (updatedImage: Image, index: number) => void; // Callback when an image's tags are updated
  onImageDelete: (deletedPath: string, index: number) => void;
}

export default function ImageModal({
  isOpen,
  onClose,
  initialImage,
  allImages,
  onImageUpdate,
  onImageDelete,
}: ImageModalProps) {
  const [currentImage, setCurrentImage] = useState<Image | null>(initialImage);
  const [currentIndex, setCurrentIndex] = useState(
    initialImage
      ? allImages.findIndex((img) => img.path === initialImage.path)
      : -1,
  );
  const [retagging, setRetagging] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [retagStatus, setRetagStatus] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  const waitForTask = async (taskId: string): Promise<any> => {
    for (let i = 0; i < 180; i++) {
      const res = await fetch(`${API_BASE_URL}/api/tasks/status?id=${encodeURIComponent(taskId)}`);
      const data = await res.json();
      if (data.state === "SUCCESS") return data;
      if (data.state === "FAILURE") throw new Error(data.message || "Task failed");
      await new Promise((resolve) => setTimeout(resolve, 500));
    }
    throw new Error("Task timeout");
  };

  // Update currentImage and currentIndex when initialImage or allImages change
  useEffect(() => {
    setCurrentImage(initialImage);
    setCurrentIndex(
      initialImage
        ? allImages.findIndex((img) => img.path === initialImage.path)
        : -1,
    );
    // Reset status when a new image is opened
    setRetagStatus(null);
  }, [initialImage, allImages]);

  const changeImage = (direction: -1 | 1) => {
    if (!allImages || allImages.length === 0) return;

    const newIndex = (currentIndex + direction + allImages.length) %
      allImages.length;
    setCurrentImage(allImages[newIndex]);
    setCurrentIndex(newIndex);
    setRetagStatus(null); // Clear retag status on image change
  };

  const handleRetag = async () => {
    if (!IS_BROWSER || !currentImage) return;

    setRetagging(true);
    setRetagStatus("Generating new tags via API...");

    try {
      const res = await fetch(`${API_BASE_URL}/api/images/retag`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ filepath: currentImage.path }),
      });
      const data = await res.json();

      if (data.success) {
        const finalData = data.task_id ? await waitForTask(data.task_id) : data;
        const result = finalData.result || finalData;
        setRetagStatus(result.message || "Tags generated successfully!");
        const updatedImage = { ...currentImage, tags: data.tags };
        updatedImage.tags = result.tags || updatedImage.tags || [];
        setCurrentImage(updatedImage);
        onImageUpdate(updatedImage, currentIndex);
      } else {
        setRetagStatus(data.message || "Failed to generate tags.");
      }
    } catch (error) {
      console.error("Retag error:", error);
      setRetagStatus(`Error: ${error.message}`);
    } finally {
      setRetagging(false);
    }
  };

  const handleDelete = async () => {
    if (!IS_BROWSER || !currentImage || deleting) return;
    if (!globalThis.confirm("Delete this image?")) return;

    setDeleting(true);
    setRetagStatus("Deleting image...");
    try {
      const res = await fetch(`${API_BASE_URL}/api/images`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ filepath: currentImage.path }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.message || "Failed to delete image");
      }
      if (data.task_id) {
        await waitForTask(data.task_id);
      }
      onImageDelete(currentImage.path, currentIndex);
      setRetagStatus("Image deleted.");
    } catch (error) {
      console.error("Delete image error:", error);
      setRetagStatus(`Error: ${error.message}`);
    } finally {
      setDeleting(false);
    }
  };

  // Keyboard navigation
  useEffect(() => {
    if (!IS_BROWSER || !isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "ArrowLeft") changeImage(-1);
      if (e.key === "ArrowRight") changeImage(1);
      if (e.key === "Escape") onClose();
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, currentIndex, allImages]);

  // Don't render anything if there's no image to display, even if open
  if (!currentImage) return null;

  return (
    <div
      class={`modal-overlay ${isOpen ? "visible" : ""}`}
      onClick={onClose}
    >
      <div
        class="modal-content"
        onClick={(e) => e.stopPropagation()} // Prevent closing when clicking inside content
      >
        <button type="button" onClick={onClose} class="modal-close">
          &times;
        </button>

        <button
          type="button"
          onClick={() => changeImage(-1)}
          class="modal-nav prev"
        >
          &#10094;
        </button>

        <img
          src={`/images/${currentImage.path}`}
          alt="Full size media"
          onClick={() => changeImage(1)} // Click on image to go next
        />

        <button
          type="button"
          onClick={() => changeImage(1)}
          class="modal-nav next"
        >
          &#10095;
        </button>

        <div class="modal-tags">
          <p>
            Tags: {currentImage.tags?.map((tag) => tag.tag).join(", ") ||
              "No tags yet."}
          </p>
          <div class="modal-actions">
            {currentImage.tags?.length === 0 && (
              <button
                type="button"
                onClick={handleRetag}
                disabled={retagging || deleting}
                class="btn btn-primary modal-action-btn"
              >
                {retagging ? "Generating..." : "Generate Tags"}
              </button>
            )}
            <button
              type="button"
              onClick={handleDelete}
              disabled={retagging || deleting}
              class="btn btn-danger modal-action-btn"
            >
              {deleting ? "Deleting..." : "Delete Image"}
            </button>
          </div>
          {retagStatus && <p class="muted-message">{retagStatus}</p>}
        </div>
      </div>
    </div>
  );
}
