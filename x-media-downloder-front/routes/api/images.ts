// x-media-downloder-front/routes/api/images.ts

import { FreshContext } from "$fresh/server.ts";
import * as path from "$std/path/mod.ts";
import { walk } from "$std/fs/walk.ts";
import { findFilesByTags, getTagsForFiles } from "../../utils/db.ts";
import type { Image } from "../../utils/types.ts";

const UPLOAD_FOLDER = "./downloaded_images";

interface ImageInfo {
  path: string;
  mtime: number | null;
}

export const handler = async (_req: Request, _ctx: FreshContext): Promise<Response> => {
  try {
    const url = new URL(_req.url);
    const sort_mode = url.searchParams.get("sort") || 'latest';
    const page = parseInt(url.searchParams.get("page") || "1");
    const per_page = parseInt(url.searchParams.get("per_page") || "100");
    const search_tags_str = url.searchParams.get("tags") || '';

    const offset = (page - 1) * per_page;

    let all_images: ImageInfo[] = [];

    const search_tags = search_tags_str.split(',').map(tag => tag.trim()).filter(Boolean);

    if (search_tags.length > 0) {
      const image_paths = findFilesByTags(search_tags);
      for (const imagePath of image_paths) {
        try {
          const fullPath = path.join(UPLOAD_FOLDER, imagePath);
          const fileInfo = await Deno.stat(fullPath);
          all_images.push({ path: imagePath, mtime: fileInfo.mtime?.getTime() ?? 0 });
        } catch {
          // Ignore files that might have been deleted
        }
      }
    } else {
      try {
        for await (const entry of walk(UPLOAD_FOLDER, { includeDirs: false, exts: [".jpg", ".jpeg", ".png", ".webp", ".gif"] })) {
          try {
            const fileInfo = await Deno.stat(entry.path);
            const relative_path = path.relative(UPLOAD_FOLDER, entry.path).replaceAll("\\", "/");
            all_images.push({ path: relative_path, mtime: fileInfo.mtime?.getTime() ?? 0 });
          } catch (e) {
            if (e instanceof Deno.errors.NotFound) {
              // File was deleted during walk, ignore it.
              continue;
            }
            throw e;
          }
        }
      } catch (e) {
        if (e instanceof Deno.errors.NotFound) {
          // The directory doesn't exist, return empty
        } else {
          throw e;
        }
      }
    }

    let images_for_page: ImageInfo[];
    const total_items = all_images.length;

    // Add a defensive filter to remove any potential null/undefined entries
    all_images = all_images.filter(Boolean);

    if (sort_mode === 'random') {
        // Simple shuffle and take first page
        all_images.sort(() => Math.random() - 0.5);
        images_for_page = all_images.slice(0, per_page);
    } else { // Default to 'latest'
        all_images.sort((a, b) => ((b?.mtime ?? 0) - (a?.mtime ?? 0)));
        images_for_page = all_images.slice(offset, offset + per_page);
    }
    
    const image_paths = images_for_page.map(img => img.path);
    const tags_map = getTagsForFiles(image_paths);

    const final_images: Image[] = images_for_page.map(img => ({
        path: img.path,
        tags: tags_map[img.path] || []
    }));

    const total_pages = total_items > 0 ? Math.ceil(total_items / per_page) : 0;

    const response = {
      items: final_images,
      total_items: total_items,
      per_page: per_page,
      current_page: page,
      total_pages: total_pages
    };

    return new Response(JSON.stringify(response), {
      headers: { "Content-Type": "application/json" },
    });

  } catch (error) {
    console.error("Error fetching images:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};
