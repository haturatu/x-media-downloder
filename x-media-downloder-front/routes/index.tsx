// x-media-downloder-front/routes/index.tsx

import { PageProps, FreshContext } from "$fresh/server.ts";
import type { Image, PagedResponse } from "../utils/types.ts";
import { getApiBaseUrl } from "../utils/api.ts";
import HomePage from "../islands/HomePage.tsx";

// Define the props for the page, which will be passed to the island
interface HomeProps {
  images: Image[];
  currentPage: number;
  totalPages: number;
}

// The page component now simply renders the island, passing data to it.
export default function HomeRoute({ data }: PageProps<HomeProps>) {
  return <HomePage {...data} />;
}

// The handler for server-side data fetching remains the same.
export const handler = async (req: Request, ctx: FreshContext): Promise<Response> => {
  const url = new URL(req.url);
  const page = parseInt(url.searchParams.get("page") || "1");
  const per_page = parseInt(url.searchParams.get("per_page") || "100");

  const API_BASE_URL = getApiBaseUrl();

  try {
    const res = await fetch(`${API_BASE_URL}/api/images?sort=latest&page=${page}&per_page=${per_page}`);
    if (!res.ok) {
      // Log the error and fall through to render with empty data
      console.error(`Error fetching images from API: ${res.statusText}`);
      const errorBody = await res.text();
      console.error(`Error body: ${errorBody}`);
      throw new Error(`HTTP error! status: ${res.status}`);
    }
    const data: PagedResponse<Image> = await res.json();
    return ctx.render({
      images: data.items || [],
      currentPage: data.current_page || 1,
      totalPages: data.total_pages || 0,
    });
  } catch (error) {
    console.error("Error fetching images in handler:", error.message);
    // In case of any error (fetch fails, API gives 500), render with empty state.
    // The island component will show the "No images found" message.
    return ctx.render({
      images: [],
      currentPage: 1,
      totalPages: 0,
    });
  }
};
