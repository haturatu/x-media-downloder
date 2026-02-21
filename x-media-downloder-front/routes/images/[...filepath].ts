// x-media-downloder-front/routes/images/[...filepath].ts

import { FreshContext } from "$fresh/server.ts";
import * as path from "$std/path/mod.ts";
import { getMimeType } from "https://deno.land/x/hono@v3.7.4/utils/mime.ts";
import { getMediaRoot } from "../../utils/media_root.ts";

const UPLOAD_FOLDER = getMediaRoot();

export const handler = async (
  _req: Request,
  ctx: FreshContext<unknown, { filepath: string }>,
): Promise<Response> => {
  try {
    const filepath = ctx.params.filepath;
    const fullPath = path.join(UPLOAD_FOLDER, filepath);

    // Basic security: prevent path traversal attacks
    if (path.normalize(fullPath) !== fullPath) {
      return new Response("Invalid path", { status: 400 });
    }

    const file = await Deno.open(fullPath, { read: true });
    const readable = file.readable;

    const contentType = getMimeType(filepath) || "application/octet-stream";

    return new Response(readable, {
      headers: { "Content-Type": contentType },
    });
  } catch (error) {
    if (error instanceof Deno.errors.NotFound) {
      return new Response("Image not found", { status: 404 });
    }
    console.error("Error serving image:", error);
    return new Response("Internal Server Error", { status: 500 });
  }
};
