import { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { IS_BROWSER } from "$fresh/runtime.ts";
import Sidebar from "../components/Sidebar.tsx";
import AutotagMenuModal from "../islands/AutotagMenuModal.tsx";
import ImageModal from "../islands/ImageModal.tsx";
import type { Image } from "../utils/types.ts";
import {
  allGalleryImages,
  selectedImage,
  selectedImageIndex,
} from "../utils/signals.ts";

interface LayoutProps {
  children: ComponentChildren;
  // We can accept route-specific info if needed, e.g. active link
  route: string;
}

export default function Layout({ children, route }: LayoutProps) {
  const [isAutotagModalOpen, setIsAutotagModalOpen] = useState(false);
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [globalStatusMessage] = useState<string | null>(null);
  const [tagSearch, setTagSearch] = useState("");

  const handleShowAutotagStatus = () => {
    if (IS_BROWSER) {
      globalThis.location.href = "/autotag-status";
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

  const handleImageDelete = (deletedPath: string, index: number) => {
    const prevImages = allGalleryImages.value;
    const updatedImages = prevImages.filter((img) => img.path !== deletedPath);
    allGalleryImages.value = updatedImages;

    if (selectedImage.value?.path !== deletedPath) {
      return;
    }

    if (updatedImages.length === 0) {
      selectedImage.value = null;
      selectedImageIndex.value = -1;
      return;
    }

    const nextIndex = Math.min(index, updatedImages.length - 1);
    selectedImage.value = updatedImages[nextIndex];
    selectedImageIndex.value = nextIndex;
  };

  const isActive = (path: string) => {
    if (path === "/") return route === "/" ? "active" : "";
    return route.startsWith(path) ? "active" : "";
  };

  const handleTagSearch = (e: Event) => {
    e.preventDefault();
    const query = tagSearch.trim();
    if (!query || !IS_BROWSER) return;
    globalThis.location.href = `/tags/${encodeURIComponent(query)}`;
  };

  return (
    <div class="container">
      <div
        class={`sidebar-backdrop ${isSidebarOpen ? "visible" : ""}`}
        onClick={() => setIsSidebarOpen(false)}
      />
      <div class={`sidebar-shell ${isSidebarOpen ? "open" : ""}`}>
        <Sidebar onNavigate={() => setIsSidebarOpen(false)} />
      </div>

      <main class="main-content">
        <header class="header">
          <div class="header-brand">
            <button
              type="button"
              class="mobile-menu-btn"
              onClick={() => setIsSidebarOpen((prev) => !prev)}
              aria-label="Toggle sidebar"
            >
              ☰
            </button>
            <h1>X Gallery</h1>
          </div>

          <div class="header-title">
            <a
              href="/"
              class={`header-link ${isActive("/")}`}
              onClick={() => setIsSidebarOpen(false)}
            >
              Home
            </a>
            <a
              href="/users"
              class={`header-link ${isActive("/users")}`}
              onClick={() => setIsSidebarOpen(false)}
            >
              Users
            </a>
            <a
              href="/tags"
              class={`header-link ${isActive("/tags")}`}
              onClick={() => setIsSidebarOpen(false)}
            >
              Tags
            </a>
            <a
              href="/download-status"
              class={`header-link ${isActive("/download-status")}`}
              onClick={() => setIsSidebarOpen(false)}
            >
              Tasks
            </a>
          </div>

          <div class="header-actions">
            <form class="tag-search-form" onSubmit={handleTagSearch}>
              <input
                type="search"
                value={tagSearch}
                placeholder="Search by tag"
                onInput={(e) => setTagSearch(e.currentTarget.value)}
              />
            </form>
            <button
              type="button"
              onClick={() => setIsAutotagModalOpen(true)}
              class="btn btn-secondary"
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
        onImageDelete={handleImageDelete}
      />
    </div>
  );
}
