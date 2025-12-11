// x-media-downloder-front/routes/api/images/retag.ts

import { FreshContext } from "$fresh/server.ts";
import * as path from "$std/path/mod.ts";
import { getTagsForFiles, addTagsForFile, markImageAsProcessed } from "../../../utils/db.ts";
import { createHash } from "https://deno.land/std@0.100.0/hash/mod.ts";


const UPLOAD_FOLDER = "./downloaded_images";
// These should be loaded from Deno.env, mirroring the python app
const AUTOTAGGER_ENABLED = Deno.env.get('AUTOTAGGER') === 'true';
const AUTOTAGGER_URL = Deno.env.get('AUTOTAGGER_URL');

async function autotagFile(fullPath: string, relativePath: string): Promise<void> {
    if (!AUTOTAGGER_ENABLED || !AUTOTAGGER_URL) {
        return;
    }

    try {
        const fileContent = await Deno.readFile(fullPath);
        
        // Hash the image to mark it as processed
        const hash = createHash("md5");
        hash.update(fileContent);
        const imageHash = hash.toString();

        const formData = new FormData();
        formData.append("file", new Blob([fileContent]), path.basename(fullPath));
        formData.append("format", "json");

        const response = await fetch(AUTOTAGGER_URL, {
            method: 'POST',
            body: formData,
        });

        if (!response.ok) {
            throw new Error(`Autotagger request failed: ${response.statusText}`);
        }

        const tagData = await response.json();

        if (Array.isArray(tagData) && tagData.length > 0) {
            const tagsToAdd: { tag: string, confidence: number }[] = [];
            const rawTags = tagData[0].tags || {};
            for (const tag in rawTags) {
                if (rawTags[tag] > 0.4) {
                    tagsToAdd.push({ tag: tag, confidence: rawTags[tag] });
                }
            }
            if (tagsToAdd.length > 0) {
                addTagsForFile(relativePath, tagsToAdd);
                markImageAsProcessed(imageHash); // Mark as processed after tagging
                console.log(`Tagged ${relativePath} with ${tagsToAdd.length} tags.`);
            }
        }
    } catch (error) {
        console.error(`Autotagging failed for ${relativePath}:`, error);
        // We don't re-throw, just log the error.
    }
}


export const handler = async (_req: Request, _ctx: FreshContext): Promise<Response> => {
    if (_req.method !== 'POST') {
        return new Response(null, { status: 405 });
    }

    try {
        const body: { filepath: string } = await _req.json();
        const filepath = body.filepath;

        if (!filepath) {
            return new Response(JSON.stringify({ success: false, message: "filepath is required" }), { status: 400 });
        }

        const existingTagsMap = getTagsForFiles([filepath]);
        if (existingTagsMap[filepath] && existingTagsMap[filepath].length > 0) {
            return new Response(JSON.stringify({
                success: true,
                message: "Image already has tags.",
                tags: existingTagsMap[filepath]
            }), { headers: { "Content-Type": "application/json" } });
        }

        const fullPath = path.join(UPLOAD_FOLDER, filepath);
        
        try {
            await Deno.stat(fullPath);
        } catch (e) {
            if (e instanceof Deno.errors.NotFound) {
                return new Response(JSON.stringify({ success: false, message: "File not found" }), { status: 404 });
            }
            throw e;
        }

        await autotagFile(fullPath, filepath);

        const newTagsMap = getTagsForFiles([filepath]);
        const newTags = newTagsMap[filepath] || [];

        return new Response(JSON.stringify({ success: true, tags: newTags }), {
            headers: { "Content-Type": "application/json" }
        });

    } catch (error) {
        console.error("Error re-tagging image:", error);
        return new Response(JSON.stringify({ error: "Internal Server Error" }), { status: 500 });
    }
};
