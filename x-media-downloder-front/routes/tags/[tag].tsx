// x-media-downloder-front/routes/tags/[tag].tsx

import { PageProps, FreshContext } from "$fresh/server.ts";
import type { Image, PagedResponse } from "../../utils/types.ts";
import { getApiBaseUrl } from "../../utils/api.ts";
import TagImagesPage from "../../islands/TagImagesPage.tsx";

interface TagImagesProps {
  tag: string;
  images: Image[];
  currentPage: number;
  totalPages: number;
}

export default function TagImagesRoute({ data }: PageProps<TagImagesProps>) {
  return <TagImagesPage {...data} />;
}

export const handler = async (req: Request, ctx: FreshContext): Promise<Response> => {
  const encodedTag = ctx.params.tag;
  const tag = decodeURIComponent(encodedTag);
  const url = new URL(req.url);
  const page = parseInt(url.searchParams.get("page") || "1");
  const per_page = parseInt(url.searchParams.get("per_page") || "100");
  const params = new URLSearchParams(url.searchParams);
  params.set("tags", tag);
  params.set("page", String(page));
  params.set("per_page", String(per_page));

  const API_BASE_URL = getApiBaseUrl();

  try {
    const res = await fetch(`${API_BASE_URL}/api/images?${params.toString()}`);
    if (!res.ok) {
      throw new Error(`HTTP error! status: ${res.status}`);
    }
    const data: PagedResponse<Image> = await res.json();
    return ctx.render({
      tag,
      images: data.items || [],
      currentPage: data.current_page || 1,
      totalPages: data.total_pages || 0,
    });
  } catch (error) {
    console.error(`Error fetching images for tag ${tag}:`, error);
    return ctx.render({
      tag,
      images: [],
      currentPage: 1,
      totalPages: 0,
    });
  }
};
