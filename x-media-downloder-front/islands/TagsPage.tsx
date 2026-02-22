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

type MatchMode = "partial" | "exact";
type TagSort = "count_desc" | "count_asc" | "name_asc" | "name_desc";

interface TagFilters {
  q: string;
  match: MatchMode;
  minCount: string;
  maxCount: string;
  sort: TagSort;
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

export default function TagsPage(props: TagsProps) {
  const {
    tags: initialTags,
    currentPage: initialCurrentPage,
    totalPages: initialTotalPages,
  } = props;

  const [tags, setTags] = useState<Tag[]>(initialTags || []);
  const [readlineTags, setReadlineTags] = useState<Tag[]>(initialTags || []);
  const [currentPage, setCurrentPage] = useState<number>(initialCurrentPage || 1);
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [deletingTag, setDeletingTag] = useState<string | null>(null);

  const [q, setQ] = useState<string>(browserParam("q", ""));
  const [match, setMatch] = useState<MatchMode>(
    browserParam("match", "partial") === "exact" ? "exact" : "partial",
  );
  const [minCount, setMinCount] = useState<string>(browserParam("min_count", ""));
  const [maxCount, setMaxCount] = useState<string>(browserParam("max_count", ""));
  const [sort, setSort] = useState<TagSort>((() => {
    const val = browserParam("sort", "count_desc");
    if (val === "count_asc" || val === "name_asc" || val === "name_desc") return val;
    return "count_desc";
  })());

  const [showAdvanced, setShowAdvanced] = useState<boolean>(false);
  const [showReadline, setShowReadline] = useState<boolean>(
    browserParam("show_readline", "0") === "1",
  );

  const API_BASE_URL = getApiBaseUrl();

  const currentFilters = (): TagFilters => ({ q, match, minCount, maxCount, sort });

  const buildParams = (page: number, filters: TagFilters): URLSearchParams => {
    const params = new URLSearchParams();
    params.set("page", String(page));
    params.set("per_page", "100");
    if (filters.q.trim()) params.set("q", filters.q.trim());
    if (filters.match !== "partial") params.set("match", filters.match);
    const min = toNonNegativeInt(filters.minCount);
    const max = toNonNegativeInt(filters.maxCount);
    if (min !== "") params.set("min_count", min);
    if (max !== "") params.set("max_count", max);
    if (filters.sort !== "count_desc") params.set("sort", filters.sort);
    if (showReadline) params.set("show_readline", "1");
    return params;
  };

  const buildReadlineParams = (filters: TagFilters): URLSearchParams => {
    const params = new URLSearchParams();
    params.set("all", "1");
    if (filters.q.trim()) params.set("q", filters.q.trim());
    if (filters.match !== "partial") params.set("match", filters.match);
    const min = toNonNegativeInt(filters.minCount);
    const max = toNonNegativeInt(filters.maxCount);
    if (min !== "") params.set("min_count", min);
    if (max !== "") params.set("max_count", max);
    if (filters.sort !== "count_desc") params.set("sort", filters.sort);
    return params;
  };

  const fetchTagsForReadline = async (
    filters: TagFilters = currentFilters(),
  ): Promise<Tag[]> => {
    const params = buildReadlineParams(filters);
    const res = await fetch(`${API_BASE_URL}/api/tags?${params.toString()}`);
    const data: PagedResponse<Tag> = await res.json();
    if (!res.ok) {
      throw new Error((data as unknown as { error?: string }).error || "Failed to fetch tags");
    }
    return data.items || [];
  };

  const fetchTags = async (page: number, filters: TagFilters = currentFilters()) => {
    setLoading(true);
    setError(null);
    try {
      const params = buildParams(page, filters);
      const res = await fetch(`${API_BASE_URL}/api/tags?${params.toString()}`);
      const data: PagedResponse<Tag> = await res.json();
      if (!res.ok) {
        throw new Error((data as unknown as { error?: string }).error || "Failed to fetch tags");
      }
      setTags(data.items || []);
      setCurrentPage(data.current_page || page);
      setTotalPages(data.total_pages || 0);
      if (showReadline) {
        const allTags = await fetchTagsForReadline(filters);
        setReadlineTags(allTags);
      } else {
        setReadlineTags(data.items || []);
      }
      globalThis.history.pushState({}, "", `/tags?${params.toString()}`);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const handlePageChange = (page: number) => {
    fetchTags(page);
  };

  const handleSearchSubmit = (e: Event) => {
    e.preventDefault();
    fetchTags(1);
  };

  const handleReset = () => {
    const reset: TagFilters = {
      q: "",
      match: "partial",
      minCount: "",
      maxCount: "",
      sort: "count_desc",
    };
    setQ(reset.q);
    setMatch(reset.match);
    setMinCount(reset.minCount);
    setMaxCount(reset.maxCount);
    setSort(reset.sort);
    fetchTags(1, reset);
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

  const handleReadlineToggle = async (checked: boolean) => {
    setShowReadline(checked);
    if (!checked) return;
    try {
      const allTags = await fetchTagsForReadline();
      setReadlineTags(allTags);
    } catch (err) {
      setError(err.message);
    }
  };

  const readlineText = readlineTags
    .map((tag) => `${tag.tag}\tcount=${tag.count || 0}`)
    .join("\n");

  useEffect(() => {
    if (!showReadline) return;
    fetchTagsForReadline()
      .then((allTags) => setReadlineTags(allTags))
      .catch((err) => setError(err.message));
  }, []);

  return (
    <>
      <Head>
        <title>Tags - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <h2 class="page-title">Tags</h2>

        <form class="advanced-search-panel" onSubmit={handleSearchSubmit}>
          <div class="advanced-search-row">
            <input
              type="search"
              class="search-box"
              placeholder="Tag (partial match)"
              value={q}
              onInput={(e) => setQ(e.currentTarget.value)}
            />
            <button type="submit" class="btn btn-primary">Search</button>
            <button type="button" class="btn" onClick={handleReset}>Reset</button>
            <label class="toggle-label">
              <input
                type="checkbox"
                checked={showAdvanced}
                onInput={(e) => setShowAdvanced(e.currentTarget.checked)}
              />
              Advanced
            </label>
            <label class="toggle-label">
              <input
                type="checkbox"
                checked={showReadline}
                onInput={(e) => handleReadlineToggle(e.currentTarget.checked)}
              />
              Readline output
            </label>
          </div>

          {showAdvanced && (
            <div class="advanced-search-grid">
              <label>
                Match
                <select value={match} onInput={(e) => setMatch(e.currentTarget.value as MatchMode)}>
                  <option value="partial">Partial</option>
                  <option value="exact">Exact</option>
                </select>
              </label>
              <label>
                Min count
                <input
                  type="number"
                  min="0"
                  value={minCount}
                  onInput={(e) => setMinCount(e.currentTarget.value)}
                />
              </label>
              <label>
                Max count
                <input
                  type="number"
                  min="0"
                  value={maxCount}
                  onInput={(e) => setMaxCount(e.currentTarget.value)}
                />
              </label>
              <label>
                Sort
                <select value={sort} onInput={(e) => setSort(e.currentTarget.value as TagSort)}>
                  <option value="count_desc">Count DESC</option>
                  <option value="count_asc">Count ASC</option>
                  <option value="name_asc">Name ASC</option>
                  <option value="name_desc">Name DESC</option>
                </select>
              </label>
            </div>
          )}
        </form>

        {showReadline && (
          <textarea
            class="readline-output"
            readOnly
            value={readlineText}
            placeholder="search results in readline format"
          />
        )}

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
