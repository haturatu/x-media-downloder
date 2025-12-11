// x-media-downloder-front/islands/UsersPage.tsx

import { Head } from "$fresh/runtime.ts";
import { useEffect, useState } from "preact/hooks";
import type { User, PagedResponse } from "../utils/types.ts";
import Pagination from "../components/Pagination.tsx";
import { getApiBaseUrl } from "../utils/api.ts";

interface UsersProps {
  users: User[];
  currentPage: number;
  totalPages: number;
}

export default function UsersPage(props: UsersProps) {
  const { users: initialUsers, currentPage: initialCurrentPage, totalPages: initialTotalPages } = props;

  const [users, setUsers] = useState<User[]>(initialUsers || []);
  const [currentPage, setCurrentPage] = useState<number>(initialCurrentPage || 1);
  const [totalPages, setTotalPages] = useState<number>(initialTotalPages || 0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  
  const API_BASE_URL = getApiBaseUrl();

  useEffect(() => {
    // Only refetch on client-side navigation
    if (currentPage !== initialCurrentPage) {
      setLoading(true);
      setError(null);
      fetch(`${API_BASE_URL}/api/users?page=${currentPage}&per_page=100`)
        .then(res => res.json())
        .then((data: PagedResponse<User>) => {
          setUsers(data.items || []);
          setTotalPages(data.total_pages || 0);
        })
        .catch(err => setError(err.message))
        .finally(() => setLoading(false));
    }
  }, [currentPage, initialCurrentPage]);

  const handlePageChange = (page: number) => {
    setCurrentPage(page);
    window.history.pushState({}, "", `/users?page=${page}`);
  };

  return (
    <>
      <Head>
        <title>Users - X Media Downloader</title>
      </Head>
      <div class="p-4">
        <h2 class="text-2xl font-bold mb-4">Users</h2>
        {loading && <p>Loading users...</p>}
        {error && <p class="text-red-500">Error: {error}</p>}
        {!users && !loading && !error && (
            <p class="text-gray-400">No users found.</p>
        )}
        {users && users.length === 0 && !loading && !error && (
          <p class="text-gray-400">No users found.</p>
        )}

        <ul class="space-y-2">
          {users && users.map((user) => (
            <li key={user.username}>
              <a href={`/users/${user.username}`} class="text-blue-400 hover:underline">
                {user.username} <span class="text-gray-500">({user.tweet_count} tweets)</span>
              </a>
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
