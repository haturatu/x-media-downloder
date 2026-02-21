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
    const normalizedRelative = filepath.replace(/^[/\\]+/, "");
    if (
      normalizedRelative.length === 0 ||
      normalizedRelative.startsWith("..")
    ) {
      return new Response("Invalid path", { status: 400 });
    }

    const uploadRoot = path.resolve(UPLOAD_FOLDER);
    const fullPath = path.resolve(uploadRoot, normalizedRelative);
    const relativeToRoot = path.relative(uploadRoot, fullPath);

    // Basic security: prevent path traversal attacks
    if (
      relativeToRoot.length === 0 ||
      relativeToRoot.startsWith("..") ||
      path.isAbsolute(relativeToRoot)
    ) {
      return new Response("Invalid path", { status: 400 });
    }

    const fileStat = await Deno.stat(fullPath);
    const mtimeMs = fileStat.mtime?.getTime() ?? 0;
    const etag = `W/\"${mtimeMs}-${fileStat.size}\"`;
    if (_req.headers.get("if-none-match") === etag) {
      return new Response(null, {
        status: 304,
        headers: {
          ETag: etag,
          "Cache-Control": "public, max-age=3600",
          "Last-Modified": new Date(mtimeMs).toUTCString(),
        },
      });
    }

    const file = await Deno.open(fullPath, { read: true });
    const readable = file.readable;

    const contentType = getMimeType(normalizedRelative) ||
      "application/octet-stream";

    return new Response(readable, {
      headers: {
        "Content-Type": contentType,
        ETag: etag,
        "Cache-Control": "public, max-age=3600",
        "Last-Modified": new Date(mtimeMs).toUTCString(),
      },
    });
  } catch (error) {
    if (error instanceof Deno.errors.NotFound) {
      return new Response("Image not found", { status: 404 });
    }
    console.error("Error serving image:", error);
    return new Response("Internal Server Error", { status: 500 });
  }
};
