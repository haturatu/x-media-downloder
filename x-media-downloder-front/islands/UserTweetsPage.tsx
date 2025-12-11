import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import type { Tweet, PagedResponse, Image } from "../utils/types.ts";
import ImageGrid from "../components/ImageGrid.tsx";
import Pagination from "../components/Pagination.tsx";
import { getApiBaseUrl } from "../utils/api.ts";
import { allGalleryImages, selectedImage, selectedImageIndex } from "../utils/signals.ts";

// Note: This interface is now what the island component expects as props.
export interface UserTweetsProps {
  username: string;
  tweets: Tweet[];
  currentPage: number;
  totalPages: number;
}

export default function UserTweetsPage(props: UserTweetsProps) {
  // The initial data is now coming from `props` instead of `data` from PageProps
  const { username: initialUsername, tweets: initialTweets, currentPage: initialCurrentPage, totalPages: initialTotalPages } = props;

  const [username] = useState<string>(initialUsername);
  const [tweets, setTweets] = useState<Tweet[]>(initialTweets || []);
  const [currentPage, setCurrentPage] = useState<number>(initialCurrentPage || 1);
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();
  const allImages = tweets.flatMap(tweet => tweet.images);

  // Update the global signal whenever the local images change
  useEffect(() => {
    allGalleryImages.value = allImages;
  }, [allImages]);

  useEffect(() => {
    // We compare with the initial prop to decide if a client-side fetch is needed
    if (currentPage !== initialCurrentPage) {
      setLoading(true);
      setError(null);
      
      fetch(`${API_BASE_URL}/api/users/${username}/tweets?page=${currentPage}&per_page=100`)
        .then(res => res.json())
        .then(data => {
          setTweets(data.items || []); // Defensive coding
          setTotalPages(data.total_pages || 0); // Defensive coding
        })
        .catch(err => setError(err.message))
        .finally(() => setLoading(false));
    }
  }, [currentPage, initialCurrentPage, username]); // Added dependencies

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
    window.history.pushState({}, "", `/users/${username}?page=${page}`);
  };

  const handleImageClick = (image: Image, index: number) => {
    selectedImage.value = image;
    selectedImageIndex.value = index;
  };

  return (
    <>
      <Head>
        <title>{username}'s Tweets - X Media Downloader</title>
      </Head>
      <div class="p-4">
        <h2 class="text-2xl font-bold mb-4">@{username}'s Images</h2>
        {loading && <p>Loading images...</p>}
        {error && <p class="text-red-500">Error: {error}</p>}
        {allImages.length === 0 && !loading && !error && (
          <p class="text-gray-400">No images found for this user.</p>
        )}

        <ImageGrid images={allImages} onImageClick={handleImageClick} />

        <Pagination
          currentPage={currentPage}
          totalPages={totalPages}
          onPageChange={handlePageChange}
        />
      </div>
    </>
  );
}
