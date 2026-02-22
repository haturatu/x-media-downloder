import { PageProps } from "$fresh/server.ts";
import type { Tweet, PagedResponse } from "../../utils/types.ts";
import { getApiBaseUrl } from "../../utils/api.ts";
import UserTweetsPage, { UserTweetsProps } from "../../islands/UserTweetsPage.tsx";

// The handler remains the same, fetching the initial data on the server.
export const handler: Fresh.Handler<UserTweetsProps> = async (req, ctx) => {
  const { username } = ctx.params;
  const url = new URL(req.url);
  const page = parseInt(url.searchParams.get("page") || "1");
  const per_page = parseInt(url.searchParams.get("per_page") || "100");
  const params = new URLSearchParams(url.searchParams);
  params.set("page", String(page));
  params.set("per_page", String(per_page));

  const API_BASE_URL = getApiBaseUrl();

  try {
    const res = await fetch(`${API_BASE_URL}/api/users/${username}/tweets?${params.toString()}`);
    if (!res.ok) {
      throw new Error(`HTTP error! status: ${res.status}`);
    }
    const data: PagedResponse<Tweet> = await res.json();
    return ctx.render({
      username,
      tweets: data.items || [], // Defensive
      currentPage: data.current_page || 1, // Defensive
      totalPages: data.total_pages || 0, // Defensive
    });
  } catch (error) {
    console.error(`Error fetching tweets for ${username}:`, error);
    // Render the page with empty data, the island will handle it
    return ctx.render({
      username,
      tweets: [],
      currentPage: 1,
      totalPages: 0,
    });
  }
};

// The new page component is now a simple wrapper that renders the island.
export default function UserTweetsRoute({ data }: PageProps<UserTweetsProps>) {
  return <UserTweetsPage {...data} />;
}
