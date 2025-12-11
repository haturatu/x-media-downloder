// x-media-downloder-front/islands/TagsPage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import type { Tag, PagedResponse } from "../utils/types.ts";
import Pagination from "../components/Pagination.tsx";
import { getApiBaseUrl } from "../utils/api.ts";

interface TagsProps {
  tags: Tag[];
  currentPage: number;
  totalPages: number;
}

export default function TagsPage(props: TagsProps) {
  const { tags: initialTags, currentPage: initialCurrentPage, totalPages: initialTotalPages } = props;

  const [tags, setTags] = useState<Tag[]>(initialTags || []);
  const [currentPage, setCurrentPage] = useState<number>(initialCurrentPage || 1);
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  useEffect(() => {
    if (currentPage !== initialCurrentPage) {
      setLoading(true);
      setError(null);
      fetch(`${API_BASE_URL}/api/tags?page=${currentPage}&per_page=100`)
        .then(res => res.json())
        .then((data: PagedResponse<Tag>) => {
          setTags(data.items || []);
          setTotalPages(data.total_pages || 0);
        })
        .catch(err => setError(err.message))
        .finally(() => setLoading(false));
    }
  }, [currentPage, initialCurrentPage]);

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
    window.history.pushState({}, "", `/tags?page=${page}`);
  };

  return (
    <>
      <Head>
        <title>Tags - X Media Downloader</title>
      </Head>
      <div class="p-4">
        <h2 class="text-2xl font-bold mb-4">Tags</h2>
        {loading && <p>Loading tags...</p>}
        {error && <p class="text-red-500">Error: {error}</p>}
        {!tags && !loading && !error && (
            <p class="text-gray-400">No tags found.</p>
        )}
        {tags && tags.length === 0 && !loading && !error && (
          <p class="text-gray-400">No tags found.</p>
        )}

        <div class="flex flex-wrap gap-2">
          {tags && tags.map((tag) => (
            <a
              key={tag.tag}
              href={`/tags/${tag.tag}`}
              class="px-3 py-1 bg-blue-600 hover:bg-blue-700 text-white text-sm rounded-full transition-colors"
            >
              {tag.tag} ({tag.count})
            </a>
          ))}
        </div>

        <Pagination
          currentPage={currentPage}
          totalPages={totalPages}
          onPageChange={handlePageChange}
        />
      </div>
    </>
  );
}
