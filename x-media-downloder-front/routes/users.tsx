// x-media-downloder-front/routes/users.tsx

import { PageProps, FreshContext } from "$fresh/server.ts";
import type { User, PagedResponse } from "../utils/types.ts";
import { getApiBaseUrl } from "../utils/api.ts";
import UsersPage from "../islands/UsersPage.tsx";

interface UsersProps {
  users: User[];
  currentPage: number;
  totalPages: number;
}

export default function UsersRoute({ data }: PageProps<UsersProps>) {
  return <UsersPage {...data} />;
}

export const handler = async (req: Request, ctx: FreshContext): Promise<Response> => {
  const url = new URL(req.url);
  const page = parseInt(url.searchParams.get("page") || "1");
  const per_page = parseInt(url.searchParams.get("per_page") || "100");

  const API_BASE_URL = getApiBaseUrl();

  try {
    const res = await fetch(`${API_BASE_URL}/api/users?page=${page}&per_page=${per_page}`);
    if (!res.ok) {
      throw new Error(`HTTP error! status: ${res.status}`);
    }
    const data: PagedResponse<User> = await res.json();
    return ctx.render({
      users: data.items,
      currentPage: data.current_page,
      totalPages: data.total_pages,
    });
  } catch (error) {
    console.error("Error fetching users:", error);
    return ctx.render({
      users: [],
      currentPage: 1,
      totalPages: 0,
    });
  }
};