// x-media-downloder-front/routes/api/tags.ts

import { FreshContext } from "$fresh/server.ts";
import { getAllTags } from "../../utils/db.ts";

export const handler = (_req: Request, _ctx: FreshContext): Response => {
  try {
    const url = new URL(_req.url);
    const page = parseInt(url.searchParams.get("page") || "1");
    const per_page = parseInt(url.searchParams.get("per_page") || "100");

    const offset = (page - 1) * per_page;

    const allTags = getAllTags();
    
    const total_items = allTags.length;
    const tags_for_page = allTags.slice(offset, offset + per_page);
    const total_pages = total_items > 0 ? Math.ceil(total_items / per_page) : 0;

    const response = {
      items: tags_for_page,
      total_items: total_items,
      per_page: per_page,
      current_page: page,
      total_pages: total_pages
    };

    return new Response(JSON.stringify(response), {
      headers: { "Content-Type": "application/json" },
    });
  } catch (error) {
    console.error("Error fetching tags:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
