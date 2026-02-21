// x-media-downloder-front/islands/UsersPage.tsx

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

export default function UsersPage(props: UsersProps) {
  const {
    users: initialUsers,
    currentPage: initialCurrentPage,
    totalPages: initialTotalPages,
  } = props;

  const [users, setUsers] = useState<User[]>(initialUsers || []);
  const [currentPage, setCurrentPage] = useState<number>(
    initialCurrentPage || 1,
  );
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [deletingUser, setDeletingUser] = useState<string | null>(null);

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

  useEffect(() => {
    // Only refetch on client-side navigation
    if (currentPage !== initialCurrentPage) {
      setLoading(true);
      setError(null);
      fetch(`${API_BASE_URL}/api/users?page=${currentPage}&per_page=100`)
        .then((res) => res.json())
        .then((data: PagedResponse<User>) => {
          setUsers(data.items || []);
          setTotalPages(data.total_pages || 0);
        })
        .catch((err) => setError(err.message))
        .finally(() => setLoading(false));
    }
  }, [currentPage, initialCurrentPage]);

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
    globalThis.history.pushState({}, "", `/users?page=${page}`);
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

  return (
    <>
      <Head>
        <title>Users - X Media Downloader</title>
      </Head>
      <div class="page-panel">
        <h2 class="page-title">Users</h2>
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
