import { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import Sidebar from "../components/Sidebar.tsx";
import AutotagMenuModal from "../islands/AutotagMenuModal.tsx";
import ImageModal from "../islands/ImageModal.tsx";
import type { Image } from "../utils/types.ts";
import { allGalleryImages, selectedImage, selectedImageIndex } from "../utils/signals.ts";

interface LayoutProps {
  children: ComponentChildren;
  // We can accept route-specific info if needed, e.g. active link
  route: string;
}

export default function Layout({ children, route }: LayoutProps) {
  const [isAutotagModalOpen, setIsAutotagModalOpen] = useState(false);
  const [globalStatusMessage, setGlobalStatusMessage] = useState<string | null>(null);

  const handleShowAutotagStatus = () => {
    if (IS_BROWSER) {
      window.location.href = "/autotag-status";
    }
  };

  const handleImageModalClose = () => {
    selectedImage.value = null;
    selectedImageIndex.value = -1;
  };

  const handleImageUpdate = (updatedImage: Image, index: number) => {
    if (index > -1 && index < allGalleryImages.value.length) {
      const updatedImages = [...allGalleryImages.value];
      updatedImages[index] = updatedImage;
      allGalleryImages.value = updatedImages;

      if (selectedImage.value?.path === updatedImage.path) {
        selectedImage.value = updatedImage;
      }
    }
  };

  const isActive = (path: string) => path === route ? 'active' : '';

  return (
    <div class="container">
      <Sidebar />

      <main class="main-content">
        <header class="header">
          <div class="header-title">
             <h1>X Gallery</h1>
            <a href="/" class={`header-link ${isActive('/')}`}>🏠 Home</a>
            <a href="/users" class={`header-link ${isActive('/users')}`}>👥 Users</a>
            <a href="/tags" class={`header-link ${isActive('/tags')}`}>🏷️ Tags</a>
          </div>
          <div class="header-actions">
            <form class="tag-search-form">
                <input
                type="search"
                placeholder="Search images by tags..."
                />
            </form>
            <button
              onClick={() => setIsAutotagModalOpen(true)}
              class="header-btn"
            >
              Autotagger
            </button>
          </div>
        </header>

        <div id="status">
          {globalStatusMessage || "Welcome to X Media Downloader!"}
        </div>

        <div class="gallery-wrapper">
          {children}
        </div>
      </main>

      <AutotagMenuModal
        isOpen={isAutotagModalOpen}
        onClose={() => setIsAutotagModalOpen(false)}
        onShowStatus={handleShowAutotagStatus}
      />

      <ImageModal
        isOpen={!!selectedImage.value}
        onClose={handleImageModalClose}
        initialImage={selectedImage.value}
        allImages={allGalleryImages.value}
        onImageUpdate={handleImageUpdate}
      />
    </div>
  );
}
