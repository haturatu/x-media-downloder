// x-media-downloder-front/islands/HomePage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import type { Image, PagedResponse } from "../utils/types.ts";
import ImageGrid from "../components/ImageGrid.tsx";
import Pagination from "../components/Pagination.tsx";
import { getApiBaseUrl } from "../utils/api.ts";
import { allGalleryImages, selectedImage, selectedImageIndex } from "../utils/signals.ts";

export interface HomePageProps {
  images: Image[];
  currentPage: number;
  totalPages: number;
}

export default function HomePage(props: HomePageProps) {
  const { images: initialImages, currentPage: initialCurrentPage, totalPages: initialTotalPages } = props;

  const [images, setImages] = useState<Image[]>(initialImages || []);
  const [currentPage, setCurrentPage] = useState<number>(initialCurrentPage || 1);
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
      fetch(`${API_BASE_URL}/api/images?sort=latest&page=${currentPage}&per_page=100`)
        .then(res => {
            if (!res.ok) throw new Error(`HTTP error! status: ${res.status}`);
            return res.json();
        })
        .then((data: PagedResponse<Image>) => {
          setImages(data.items || []);
          setTotalPages(data.total_pages || 0);
        })
        .catch(err => setError(err.message))
        .finally(() => setLoading(false));
    }
  }, [currentPage, initialCurrentPage]);

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
    window.history.pushState({}, "", `/?page=${page}`);
  };

  const handleImageClick = (image: Image, index: number) => {
    selectedImage.value = image;
    selectedImageIndex.value = index;
  };

  return (
    <>
      <Head>
        <title>Home - X Media Downloader</title>
      </Head>
      <div class="p-4">
        <h2 class="text-2xl font-bold mb-4">Latest Posts</h2>
        {loading && <p>Loading images...</p>}
        {error && <p class="text-red-500">Error: {error}</p>}
        {images.length === 0 && !loading && !error && (
          <p class="text-gray-400">No images found. Start by downloading some!</p>
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
