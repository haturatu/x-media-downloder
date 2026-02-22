// x-media-downloder-front/routes/tags.tsx

import { PageProps, FreshContext } from "$fresh/server.ts";
import type { Tag, PagedResponse } from "../utils/types.ts";
import { getApiBaseUrl } from "../utils/api.ts";
import TagsPage from "../islands/TagsPage.tsx";

interface TagsProps {
  tags: Tag[];
  currentPage: number;
  totalPages: number;
}

export default function TagsRoute({ data }: PageProps<TagsProps>) {
  return <TagsPage {...data} />;
}

export const handler = async (req: Request, ctx: FreshContext): Promise<Response> => {
  const url = new URL(req.url);
  const params = new URLSearchParams(url.searchParams);
  if (!params.get("page")) params.set("page", "1");
  if (!params.get("per_page")) params.set("per_page", "100");

  const API_BASE_URL = getApiBaseUrl();

  try {
    const res = await fetch(`${API_BASE_URL}/api/tags?${params.toString()}`);
    if (!res.ok) {
      throw new Error(`HTTP error! status: ${res.status}`);
    }
    const data: PagedResponse<Tag> = await res.json();
    return ctx.render({
      tags: data.items,
      currentPage: data.current_page,
      totalPages: data.total_pages,
    });
  } catch (error) {
    console.error("Error fetching tags:", error);
    return ctx.render({
      tags: [],
      currentPage: 1,
      totalPages: 0,
    });
  }
};
