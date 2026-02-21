// x-media-downloder-front/routes/api/users.ts

import { FreshContext } from "$fresh/server.ts";
import * as path from "$std/path/mod.ts";
import { walk } from "$std/fs/walk.ts";
import { deleteTagsForUser } from "../../utils/db.ts";
import { getMediaRoot } from "../../utils/media_root.ts";
import {
  buildCacheKey,
  getCachedValue,
  invalidateCacheByPrefix,
  setCachedValue,
} from "../../utils/response_cache.ts";

const UPLOAD_FOLDER = getMediaRoot();

interface UserInfo {
  username: string;
  tweet_count: number;
}

interface DeleteUserRequest {
  username: string;
}

export const handler = async (
  _req: Request,
  _ctx: FreshContext,
): Promise<Response> => {
  if (_req.method === "DELETE") {
    try {
      const body: DeleteUserRequest = await _req.json();
      const username = body.username?.trim();
      if (!username) {
        return new Response(
          JSON.stringify({ success: false, message: "username is required" }),
          { status: 400, headers: { "Content-Type": "application/json" } },
        );
      }

      if (username.includes("/") || username.includes("\\")) {
        return new Response(
          JSON.stringify({ success: false, message: "Invalid username" }),
          { status: 400, headers: { "Content-Type": "application/json" } },
        );
      }

      const userPath = path.join(UPLOAD_FOLDER, username);

      let imageCount = 0;
      try {
        for await (
          const _entry of walk(userPath, {
            includeDirs: false,
            exts: [".jpg", ".jpeg", ".png", ".webp", ".gif"],
          })
        ) {
          imageCount++;
        }
      } catch {
        // Count is best-effort only.
      }

      try {
        await Deno.remove(userPath, { recursive: true });
      } catch (e) {
        if (e instanceof Deno.errors.NotFound) {
          return new Response(
            JSON.stringify({ success: false, message: "User not found" }),
            { status: 404, headers: { "Content-Type": "application/json" } },
          );
        }
        throw e;
      }

      deleteTagsForUser(username);
      invalidateCacheByPrefix("/api/users?");
      invalidateCacheByPrefix("/api/users/");
      invalidateCacheByPrefix("/api/images?");
      invalidateCacheByPrefix("/api/tags?");

      return new Response(
        JSON.stringify({
          success: true,
          message: `Deleted user '${username}' and ${imageCount} images`,
          username,
          deleted_images: imageCount,
        }),
        { headers: { "Content-Type": "application/json" } },
      );
    } catch (error) {
      console.error("Error deleting user:", error);
      return new Response(JSON.stringify({ error: "Internal Server Error" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      });
    }
  }

  if (_req.method !== "GET") {
    return new Response(null, { status: 405 });
  }

  try {
    const url = new URL(_req.url);
    const cacheKey = buildCacheKey(url.pathname, url.searchParams.toString());
    const cachedResponse = getCachedValue<{
      items: UserInfo[];
      total_items: number;
      per_page: number;
      current_page: number;
      total_pages: number;
    }>(cacheKey);
    if (cachedResponse) {
      return new Response(JSON.stringify(cachedResponse), {
        headers: {
          "Content-Type": "application/json",
          "X-Cache": "HIT",
        },
      });
    }

    const page = parseInt(url.searchParams.get("page") || "1");
    const per_page = parseInt(url.searchParams.get("per_page") || "100");
    const search_query = url.searchParams.get("q")?.toLowerCase() || "";

    const offset = (page - 1) * per_page;

    const all_users: UserInfo[] = [];
    try {
      for await (const userEntry of Deno.readDir(UPLOAD_FOLDER)) {
        if (!userEntry.isDirectory) continue;
        if (
          search_query && !userEntry.name.toLowerCase().includes(search_query)
        ) continue;

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
      total_pages: total_pages,
    };
    setCachedValue(cacheKey, response);

    return new Response(JSON.stringify(response), {
      headers: {
        "Content-Type": "application/json",
        "X-Cache": "MISS",
      },
    });
  } catch (error) {
    console.error("Error listing users:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
