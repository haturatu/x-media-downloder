// x-media-downloder-front/routes/api/users.ts

import { FreshContext } from "$fresh/server.ts";
import * as path from "$std/path/mod.ts";

const UPLOAD_FOLDER = "./downloaded_images";

interface UserInfo {
  username: string;
  tweet_count: number;
}

export const handler = async (_req: Request, _ctx: FreshContext): Promise<Response> => {
  try {
    const url = new URL(_req.url);
    const page = parseInt(url.searchParams.get("page") || "1");
    const per_page = parseInt(url.searchParams.get("per_page") || "100");
    const search_query = url.searchParams.get("q")?.toLowerCase() || '';

    const offset = (page - 1) * per_page;

    const all_users: UserInfo[] = [];
    try {
      for await (const userEntry of Deno.readDir(UPLOAD_FOLDER)) {
        if (!userEntry.isDirectory) continue;
        if (search_query && !userEntry.name.toLowerCase().includes(search_query)) continue;
        
        const user_path = path.join(UPLOAD_FOLDER, userEntry.name);
        let tweet_count = 0;
        try {
          for await (const tweetEntry of Deno.readDir(user_path)) {
            if (tweetEntry.isDirectory) {
              tweet_count++;
            }
          }
        } catch {
          // Ignore errors reading subdirectories, maybe permissions issue
        }
        
        if (tweet_count > 0) {
          all_users.push({ username: userEntry.name, tweet_count });
        }
      }
    } catch (e) {
      if (e instanceof Deno.errors.NotFound) {
        // The directory doesn't exist, return empty
      } else {
        throw e;
      }
    }

    // Sort users alphabetically
    all_users.sort((a, b) => a.username.localeCompare(b.username));

    const total_items = all_users.length;
    const users_for_page = all_users.slice(offset, offset + per_page);
    const total_pages = total_items > 0 ? Math.ceil(total_items / per_page) : 0;
    
    const response = {
      items: users_for_page,
      total_items: total_items,
      per_page: per_page,
      current_page: page,
      total_pages: total_pages
    };

    return new Response(JSON.stringify(response), {
      headers: { "Content-Type": "application/json" },
    });

  } catch (error) {
    console.error("Error listing users:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
