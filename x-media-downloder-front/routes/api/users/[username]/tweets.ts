// x-media-downloder-front/routes/api/users/[username]/tweets.ts

import { FreshContext } from "$fresh/server.ts";
import * as path from "$std/path/mod.ts";
import { getTagsForFiles } from "../../../../utils/db.ts";
import type { Image, Tweet } from "../../../../utils/types.ts";

const UPLOAD_FOLDER = "./downloaded_images";

export const handler = async (
  _req: Request,
  ctx: FreshContext<unknown, { username: string }>,
): Promise<Response> => {
  try {
    const { username } = ctx.params;
    const url = new URL(_req.url);
    const page = parseInt(url.searchParams.get("page") || "1");
    const per_page = parseInt(url.searchParams.get("per_page") || "100");
    const offset = (page - 1) * per_page;

    const user_path = path.join(UPLOAD_FOLDER, username);
    const all_tweets: Tweet[] = [];

    try {
      // Read all tweet directories for the user
      const tweetDirs = [];
      for await (const entry of Deno.readDir(user_path)) {
        if (entry.isDirectory) {
          tweetDirs.push(entry.name);
        }
      }
      // Sort tweet IDs reverse-chronologically (assuming they are sortable strings like IDs)
      tweetDirs.sort().reverse();

      for (const tweet_id of tweetDirs) {
        const tweet_path = path.join(user_path, tweet_id);
        const images_in_tweet: Image[] = [];
        const image_paths_in_tweet: string[] = [];
        
        for await (const img_entry of Deno.readDir(tweet_path)) {
           if (img_entry.isFile && /\.(jpg|jpeg|png|webp|gif)$/i.test(img_entry.name)) {
                const full_path = path.join(tweet_path, img_entry.name);
                const relative_path = path.relative(UPLOAD_FOLDER, full_path).replaceAll("\\", "/");
                image_paths_in_tweet.push(relative_path);
                // Tags will be added below
                images_in_tweet.push({ path: relative_path, tags: [] });
           }
        }

        if (images_in_tweet.length > 0) {
            const tags_map = getTagsForFiles(image_paths_in_tweet);
            for (const img of images_in_tweet) {
                img.tags = tags_map[img.path] || [];
            }
            all_tweets.push({ tweet_id, images: images_in_tweet });
        }
      }
    } catch (e) {
      if (e instanceof Deno.errors.NotFound) {
        return new Response(JSON.stringify({ error: "User not found" }), {
          status: 404,
          headers: { "Content-Type": "application/json" },
        });
      }
      throw e;
    }

    const total_items = all_tweets.length;
    const tweets_for_page = all_tweets.slice(offset, offset + per_page);
    const total_pages = total_items > 0 ? Math.ceil(total_items / per_page) : 0;
    
    const response = {
      items: tweets_for_page,
      total_items: total_items,
      per_page: per_page,
      current_page: page,
      total_pages: total_pages
    };

    return new Response(JSON.stringify(response), {
      headers: { "Content-Type": "application/json" },
    });

  } catch (error) {
    console.error("Error listing user tweets:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
