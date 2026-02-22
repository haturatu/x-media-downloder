// x-media-downloder-front/islands/TagImagesPage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import type { Image } from "../utils/types.ts";
import ImageGrid from "../components/ImageGrid.tsx";
import Pagination from "../components/Pagination.tsx";
import { getApiBaseUrl } from "../utils/api.ts";
import {
  allGalleryImages,
  selectedImage,
  selectedImageIndex,
} from "../utils/signals.ts";

interface TagImagesProps {
  tag: string;
  images: Image[];
  currentPage: number;
  totalPages: number;
}

interface TagImageFilters {
  minTagCount: string;
  maxTagCount: string;
}

function browserParam(key: string, fallback: string): string {
  if (typeof globalThis.location === "undefined") return fallback;
  const value = new URLSearchParams(globalThis.location.search).get(key);
  return value ?? fallback;
}

function toNonNegativeInt(value: string): string {
  const trimmed = value.trim();
  if (trimmed === "") return "";
  const parsed = Number.parseInt(trimmed, 10);
  if (!Number.isFinite(parsed) || parsed < 0) return "";
  return String(parsed);
}

export default function TagImagesPage(props: TagImagesProps) {
  const {
    tag,
    images: initialImages,
    currentPage: initialCurrentPage,
    totalPages: initialTotalPages,
  } = props;

  const [images, setImages] = useState<Image[]>(initialImages || []);
  const [currentPage, setCurrentPage] = useState<number>(
    initialCurrentPage || 1,
  );
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [deletingTag, setDeletingTag] = useState<boolean>(false);
  const [minTagCount, setMinTagCount] = useState<string>(
    browserParam("min_tag_count", ""),
  );
  const [maxTagCount, setMaxTagCount] = useState<string>(
    browserParam("max_tag_count", ""),
  );

  const API_BASE_URL = getApiBaseUrl();
  const currentFilters = (): TagImageFilters => ({ minTagCount, maxTagCount });

  const buildParams = (page: number, filters: TagImageFilters): URLSearchParams => {
    const params = new URLSearchParams();
    params.set("tags", tag);
    params.set("page", String(page));
    params.set("per_page", "100");
    const min = toNonNegativeInt(filters.minTagCount);
    const max = toNonNegativeInt(filters.maxTagCount);
    if (min !== "") params.set("min_tag_count", min);
    if (max !== "") params.set("max_tag_count", max);
    return params;
  };

  const fetchImages = async (
    page: number,
    filters: TagImageFilters = currentFilters(),
  ) => {
    setLoading(true);
    setError(null);
    try {
      const params = buildParams(page, filters);
      const res = await fetch(`${API_BASE_URL}/api/images?${params.toString()}`);
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || "Failed to fetch images");
      }
      setImages(data.items || []);
      setCurrentPage(data.current_page || page);
      setTotalPages(data.total_pages || 0);
      globalThis.history.pushState({}, "", `/tags/${encodeURIComponent(tag)}?${params.toString()}`);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  // Update the global signal whenever the local images change
  useEffect(() => {
    allGalleryImages.value = images;
  }, [images]);

  useEffect(() => {
    if (currentPage !== initialCurrentPage) {
      fetchImages(currentPage);
    }
  }, [currentPage, initialCurrentPage, tag]);

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
  };

  const handleSearchSubmit = (e: Event) => {
    e.preventDefault();
    fetchImages(1);
  };

  const handleReset = () => {
    setMinTagCount("");
    setMaxTagCount("");
    fetchImages(1, { minTagCount: "", maxTagCount: "" });
  };

  const handleImageClick = (image: Image, index: number) => {
    selectedImage.value = image;
    selectedImageIndex.value = index;
  };

  const handleDeleteTag = async () => {
    if (!globalThis.confirm(`Delete tag "${tag}" from all images?`)) {
      return;
    }
    setDeletingTag(true);
    setError(null);
    try {
      const res = await fetch(`${API_BASE_URL}/api/tags`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ tag }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.message || "Failed to delete tag");
      }
      globalThis.location.href = "/tags";
    } catch (err) {
      setError(err.message);
      setDeletingTag(false);
    }
  };

  return (
    <>
      <Head>
        <title>Tag: {tag} - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <div class="status-head">
          <h2 class="page-title">Images tagged with "{tag}"</h2>
          <button
            type="button"
            class="btn btn-danger"
            disabled={deletingTag}
            onClick={handleDeleteTag}
          >
            {deletingTag ? "Deleting..." : "Delete This Tag"}
          </button>
        </div>
        <form class="advanced-search-panel" onSubmit={handleSearchSubmit}>
          <div class="advanced-search-row">
            <input
              type="number"
              min="0"
              class="search-box"
              placeholder="Min tags per image"
              value={minTagCount}
              onInput={(e) => setMinTagCount(e.currentTarget.value)}
            />
            <input
              type="number"
              min="0"
              class="search-box"
              placeholder="Max tags per image"
              value={maxTagCount}
              onInput={(e) => setMaxTagCount(e.currentTarget.value)}
            />
            <button type="submit" class="btn btn-primary">Search</button>
            <button type="button" class="btn" onClick={handleReset}>Reset</button>
          </div>
        </form>
        {loading && <p>Loading images...</p>}
        {error && <p class="error-text">Error: {error}</p>}
        {images.length === 0 && !loading && !error && (
          <p class="info-text">No images found for this tag.</p>
        )}

        <ImageGrid images={images} onImageClick={handleImageClick} />

        <Pagination
          currentPage={currentPage}
          totalPages={totalPages}
          onPageChange={handlePageChange}
        />
      </div>
    </>
  );
}
