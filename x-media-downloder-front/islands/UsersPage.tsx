import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import type { PagedResponse, User } from "../utils/types.ts";
import Pagination from "../components/Pagination.tsx";
import { getApiBaseUrl } from "../utils/api.ts";

interface UsersProps {
  users: User[];
  currentPage: number;
  totalPages: number;
}

type MatchMode = "partial" | "exact";
type UserSort = "name_asc" | "name_desc" | "tweets_desc" | "tweets_asc";

interface UserFilters {
  q: string;
  match: MatchMode;
  minTweets: string;
  maxTweets: string;
  sort: UserSort;
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

export default function UsersPage(props: UsersProps) {
  const {
    users: initialUsers,
    currentPage: initialCurrentPage,
    totalPages: initialTotalPages,
  } = props;

  const [users, setUsers] = useState<User[]>(initialUsers || []);
  const [readlineUsers, setReadlineUsers] = useState<User[]>(initialUsers || []);
  const [currentPage, setCurrentPage] = useState<number>(initialCurrentPage || 1);
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [deletingUser, setDeletingUser] = useState<string | null>(null);

  const [q, setQ] = useState<string>(browserParam("q", ""));
  const [match, setMatch] = useState<MatchMode>(
    browserParam("match", "partial") === "exact" ? "exact" : "partial",
  );
  const [minTweets, setMinTweets] = useState<string>(browserParam("min_tweets", ""));
  const [maxTweets, setMaxTweets] = useState<string>(browserParam("max_tweets", ""));
  const [sort, setSort] = useState<UserSort>((() => {
    const val = browserParam("sort", "name_asc");
    if (val === "name_desc" || val === "tweets_desc" || val === "tweets_asc") return val;
    return "name_asc";
  })());

  const [showAdvanced, setShowAdvanced] = useState<boolean>(false);
  const [showReadline, setShowReadline] = useState<boolean>(
    browserParam("show_readline", "0") === "1",
  );

  const API_BASE_URL = getApiBaseUrl();

  const waitForTask = async (taskId: string): Promise<any> => {
    for (let i = 0; i < 120; i++) {
      const res = await fetch(`${API_BASE_URL}/api/tasks/status?id=${encodeURIComponent(taskId)}`);
      const data = await res.json();
      if (data.state === "SUCCESS") return data;
      if (data.state === "FAILURE") {
        throw new Error(data.message || "Task failed");
      }
      await new Promise((resolve) => setTimeout(resolve, 500));
    }
    throw new Error("Task timeout");
  };

  const currentFilters = (): UserFilters => ({ q, match, minTweets, maxTweets, sort });

  const buildParams = (page: number, filters: UserFilters): URLSearchParams => {
    const params = new URLSearchParams();
    params.set("page", String(page));
    params.set("per_page", "100");
    if (filters.q.trim()) params.set("q", filters.q.trim());
    if (filters.match !== "partial") params.set("match", filters.match);
    const min = toNonNegativeInt(filters.minTweets);
    const max = toNonNegativeInt(filters.maxTweets);
    if (min !== "") params.set("min_tweets", min);
    if (max !== "") params.set("max_tweets", max);
    if (filters.sort !== "name_asc") params.set("sort", filters.sort);
    if (showReadline) params.set("show_readline", "1");
    return params;
  };

  const buildReadlineParams = (filters: UserFilters): URLSearchParams => {
    const params = new URLSearchParams();
    params.set("all", "1");
    if (filters.q.trim()) params.set("q", filters.q.trim());
    if (filters.match !== "partial") params.set("match", filters.match);
    const min = toNonNegativeInt(filters.minTweets);
    const max = toNonNegativeInt(filters.maxTweets);
    if (min !== "") params.set("min_tweets", min);
    if (max !== "") params.set("max_tweets", max);
    if (filters.sort !== "name_asc") params.set("sort", filters.sort);
    return params;
  };

  const fetchUsersForReadline = async (
    filters: UserFilters = currentFilters(),
  ): Promise<User[]> => {
    const params = buildReadlineParams(filters);
    const res = await fetch(`${API_BASE_URL}/api/users?${params.toString()}`);
    const data: PagedResponse<User> = await res.json();
    if (!res.ok) {
      throw new Error((data as unknown as { error?: string }).error || "Failed to fetch users");
    }
    return data.items || [];
  };

  const fetchUsers = async (page: number, filters: UserFilters = currentFilters()) => {
    setLoading(true);
    setError(null);
    try {
      const params = buildParams(page, filters);
      const res = await fetch(`${API_BASE_URL}/api/users?${params.toString()}`);
      const data: PagedResponse<User> = await res.json();
      if (!res.ok) {
        throw new Error((data as unknown as { error?: string }).error || "Failed to fetch users");
      }
      setUsers(data.items || []);
      setCurrentPage(data.current_page || page);
      setTotalPages(data.total_pages || 0);
      if (showReadline) {
        const allUsers = await fetchUsersForReadline(filters);
        setReadlineUsers(allUsers);
      } else {
        setReadlineUsers(data.items || []);
      }
      globalThis.history.pushState({}, "", `/users?${params.toString()}`);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const handlePageChange = (page: number) => {
    fetchUsers(page);
  };

  const handleSearchSubmit = (e: Event) => {
    e.preventDefault();
    fetchUsers(1);
  };

  const handleReset = () => {
    const reset: UserFilters = {
      q: "",
      match: "partial",
      minTweets: "",
      maxTweets: "",
      sort: "name_asc",
    };
    setQ(reset.q);
    setMatch(reset.match);
    setMinTweets(reset.minTweets);
    setMaxTweets(reset.maxTweets);
    setSort(reset.sort);
    fetchUsers(1, reset);
  };

  const handleDeleteUser = async (username: string) => {
    if (!globalThis.confirm(`Delete all images and tags for @${username}?`)) {
      return;
    }

    setDeletingUser(username);
    setError(null);
    try {
      const res = await fetch(`${API_BASE_URL}/api/users`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username }),
      });
      const data = await res.json();
      if (!res.ok || !data.success) {
        throw new Error(data.message || "Failed to delete user");
      }
      if (data.task_id) {
        await waitForTask(data.task_id);
      }

      setUsers((prev) => prev.filter((user) => user.username !== username));
    } catch (err) {
      setError(err.message);
    } finally {
      setDeletingUser(null);
    }
  };

  const handleReadlineToggle = async (checked: boolean) => {
    setShowReadline(checked);
    if (!checked) return;
    try {
      const allUsers = await fetchUsersForReadline();
      setReadlineUsers(allUsers);
    } catch (err) {
      setError(err.message);
    }
  };

  const readlineText = readlineUsers
    .map((user) => `${user.username}\ttweet_count=${user.tweet_count}`)
    .join("\n");

  useEffect(() => {
    if (!showReadline) return;
    fetchUsersForReadline()
      .then((allUsers) => setReadlineUsers(allUsers))
      .catch((err) => setError(err.message));
  }, []);

  return (
    <>
      <Head>
        <title>Users - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <h2 class="page-title">Users</h2>

        <form class="advanced-search-panel" onSubmit={handleSearchSubmit}>
          <div class="advanced-search-row">
            <input
              type="search"
              class="search-box"
              placeholder="Username (partial match)"
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
                Min tweets
                <input
                  type="number"
                  min="0"
                  value={minTweets}
                  onInput={(e) => setMinTweets(e.currentTarget.value)}
                />
              </label>
              <label>
                Max tweets
                <input
                  type="number"
                  min="0"
                  value={maxTweets}
                  onInput={(e) => setMaxTweets(e.currentTarget.value)}
                />
              </label>
              <label>
                Sort
                <select value={sort} onInput={(e) => setSort(e.currentTarget.value as UserSort)}>
                  <option value="name_asc">Name ASC</option>
                  <option value="name_desc">Name DESC</option>
                  <option value="tweets_desc">Tweets DESC</option>
                  <option value="tweets_asc">Tweets ASC</option>
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

        {loading && <p>Loading users...</p>}
        {error && <p class="error-text">Error: {error}</p>}
        {!users && !loading && !error && (
          <p class="info-text">No users found.</p>
        )}
        {users && users.length === 0 && !loading && !error && (
          <p class="info-text">No users found.</p>
        )}

        <ul class="users-list">
          {users &&
            users.map((user) => (
              <li key={user.username} class="users-item">
                <div class="users-item-main">
                  <a href={`/users/${user.username}`} class="users-link">
                    {user.username}{" "}
                    <span class="users-meta">({user.tweet_count} tweets)</span>
                  </a>
                  <button
                    type="button"
                    class="btn btn-danger users-delete-btn"
                    disabled={deletingUser === user.username}
                    onClick={() => handleDeleteUser(user.username)}
                  >
                    {deletingUser === user.username ? "Deleting..." : "Delete"}
                  </button>
                </div>
              </li>
            ))}
        </ul>

        <Pagination
          currentPage={currentPage}
          totalPages={totalPages}
          onPageChange={handlePageChange}
        />
      </div>
    </>
  );
}
