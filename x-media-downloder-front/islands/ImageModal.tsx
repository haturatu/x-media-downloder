import { useState, useEffect } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import type { Image } from "../utils/types.ts";
import { getApiBaseUrl } from "../utils/api.ts";

interface ImageModalProps {
  isOpen: boolean;
  onClose: () => void;
  initialImage: Image | null;
  allImages: Image[]; // All images in the current gallery for navigation
  onImageUpdate: (updatedImage: Image, index: number) => void; // Callback when an image's tags are updated
}

export default function ImageModal({
  isOpen,
  onClose,
  initialImage,
  allImages,
  onImageUpdate,
}: ImageModalProps) {
  const [currentImage, setCurrentImage] = useState<Image | null>(initialImage);
  const [currentIndex, setCurrentIndex] = useState(
    initialImage ? allImages.findIndex((img) => img.path === initialImage.path) : -1,
  );
  const [retagging, setRetagging] = useState(false);
  const [retagStatus, setRetagStatus] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  // Update currentImage and currentIndex when initialImage or allImages change
  useEffect(() => {
    setCurrentImage(initialImage);
    setCurrentIndex(
      initialImage ? allImages.findIndex((img) => img.path === initialImage.path) : -1,
    );
    // Reset status when a new image is opened
    setRetagStatus(null);
  }, [initialImage, allImages]);

  const changeImage = (direction: -1 | 1) => {
    if (!allImages || allImages.length === 0) return;

    const newIndex = (currentIndex + direction + allImages.length) % allImages.length;
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
        setRetagStatus(data.message || "Tags generated successfully!");
        const updatedImage = { ...currentImage, tags: data.tags };
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
        <button onClick={onClose} class="modal-close">&times;</button>

        <button onClick={() => changeImage(-1)} class="modal-nav prev">&#10094;</button>

        <img
          src={`/images/${currentImage.path}`}
          alt="Full size media"
          onClick={() => changeImage(1)} // Click on image to go next
        />
        
        <button onClick={() => changeImage(1)} class="modal-nav next">&#10095;</button>

        <div class="modal-tags">
          <p>
            Tags: {currentImage.tags?.map((tag) => tag.tag).join(", ") || "No tags yet."}
          </p>
          {currentImage.tags?.length === 0 && (
             <button
              onClick={handleRetag}
              disabled={retagging}
              class="header-btn" // Re-using button style
              style={{marginTop: '0.5rem', backgroundColor: retagging ? '#444' : '#007bff'}}
            >
              {retagging ? "Generating..." : "Generate Tags"}
            </button>
          )}
          {retagStatus && <p style={{fontSize: '0.8rem', color: '#888', marginTop: '0.5rem'}}>{retagStatus}</p>}
        </div>
      </div>
    </div>
  );
}
