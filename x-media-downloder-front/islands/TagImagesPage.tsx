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

  const API_BASE_URL = getApiBaseUrl();

  // Update the global signal whenever the local images change
  useEffect(() => {
    allGalleryImages.value = images;
  }, [images]);

  useEffect(() => {
    if (currentPage !== initialCurrentPage) {
      setLoading(true);
      setError(null);

      fetch(
        `${API_BASE_URL}/api/images?tags=${
          encodeURIComponent(tag)
        }&page=${currentPage}&per_page=100`,
      )
        .then((res) => res.json())
        .then((data) => {
          setImages(data.items || []);
          setTotalPages(data.total_pages || 0);
        })
        .catch((err) => setError(err.message))
        .finally(() => setLoading(false));
    }
  }, [currentPage, initialCurrentPage, tag]);

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
    globalThis.history.pushState({}, "", `/tags/${tag}?page=${page}`);
  };

  const handleImageClick = (image: Image, index: number) => {
    selectedImage.value = image;
    selectedImageIndex.value = index;
  };

  return (
    <>
      <Head>
        <title>Tag: {tag} - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <h2 class="page-title">Images tagged with "{tag}"</h2>
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
