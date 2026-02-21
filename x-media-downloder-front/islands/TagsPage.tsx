// x-media-downloder-front/islands/TagsPage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import type { PagedResponse, Tag } from "../utils/types.ts";
import Pagination from "../components/Pagination.tsx";
import { getApiBaseUrl } from "../utils/api.ts";

interface TagsProps {
  tags: Tag[];
  currentPage: number;
  totalPages: number;
}

export default function TagsPage(props: TagsProps) {
  const {
    tags: initialTags,
    currentPage: initialCurrentPage,
    totalPages: initialTotalPages,
  } = props;

  const [tags, setTags] = useState<Tag[]>(initialTags || []);
  const [currentPage, setCurrentPage] = useState<number>(
    initialCurrentPage || 1,
  );
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [deletingTag, setDeletingTag] = useState<string | null>(null);

  const API_BASE_URL = getApiBaseUrl();

  useEffect(() => {
    if (currentPage !== initialCurrentPage) {
      setLoading(true);
      setError(null);
      fetch(`${API_BASE_URL}/api/tags?page=${currentPage}&per_page=100`)
        .then((res) => res.json())
        .then((data: PagedResponse<Tag>) => {
          setTags(data.items || []);
          setTotalPages(data.total_pages || 0);
        })
        .catch((err) => setError(err.message))
        .finally(() => setLoading(false));
    }
  }, [currentPage, initialCurrentPage]);

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
    globalThis.history.pushState({}, "", `/tags?page=${page}`);
  };

  const handleDeleteTag = async (tag: string) => {
    if (!globalThis.confirm(`Delete tag "${tag}" from all images?`)) {
      return;
    }
    setDeletingTag(tag);
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
      setTags((prev) => prev.filter((item) => item.tag !== tag));
    } catch (err) {
      setError(err.message);
    } finally {
      setDeletingTag(null);
    }
  };

  return (
    <>
      <Head>
        <title>Tags - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <h2 class="page-title">Tags</h2>
        {loading && <p>Loading tags...</p>}
        {error && <p class="error-text">Error: {error}</p>}
        {!tags && !loading && !error && <p class="info-text">No tags found.</p>}
        {tags && tags.length === 0 && !loading && !error && (
          <p class="info-text">No tags found.</p>
        )}

        <div class="tag-chip-list">
          {tags && tags.map((tag) => (
            <div key={tag.tag} class="tag-chip">
              <a href={`/tags/${encodeURIComponent(tag.tag)}`}>
                {tag.tag} ({tag.count})
              </a>
              <button
                type="button"
                class="btn btn-danger users-delete-btn"
                disabled={deletingTag === tag.tag}
                onClick={() => handleDeleteTag(tag.tag)}
              >
                {deletingTag === tag.tag ? "Deleting..." : "Delete"}
              </button>
            </div>
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
