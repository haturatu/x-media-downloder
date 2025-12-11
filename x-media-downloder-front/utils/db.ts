// x-media-downloder-front/utils/db.ts

import { DB } from "sqlite";
import { Tag } from "./types.ts";

const DATABASE_PATH = "./tags.db";

// The DB instance is shared across the application.
// Deno's module system ensures this is a singleton.
let db: DB | null = null;

function getDb(): DB {
  if (db) {
    return db;
  }
  db = new DB(DATABASE_PATH);
  return db;
}

export function initDb() {
  const conn = getDb();
  
  // Main table for tags
  conn.execute(`
    CREATE TABLE IF NOT EXISTS image_tags (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        filepath TEXT NOT NULL,
        tag TEXT NOT NULL,
        confidence REAL,
        UNIQUE(filepath, tag)
    );
  `);
  
  // Table to track processed images by their content hash
  conn.execute(`
    CREATE TABLE IF NOT EXISTS processed_images (
        image_hash TEXT PRIMARY KEY
    );
  `);
  
  console.log("Database initialized.");
}

// Ensure DB is initialized on module load
// initDb();


export function addTagsForFile(filepath: string, tags: { tag: string; confidence: number }[]) {
  const conn = getDb();
  for (const tagInfo of tags) {
    if (tagInfo.tag) {
      conn.query(
        "INSERT OR IGNORE INTO image_tags (filepath, tag, confidence) VALUES (?, ?, ?)",
        [filepath, tagInfo.tag, tagInfo.confidence]
      );
    }
  }
}

export function markImageAsProcessed(imageHash: string) {
  const conn = getDb();
  conn.query("INSERT OR IGNORE INTO processed_images (image_hash) VALUES (?)", [imageHash]);
}

export function isImageProcessed(imageHash: string): boolean {
  const conn = getDb();
  const [result] = conn.query<[number]>("SELECT 1 FROM processed_images WHERE image_hash = ?", [imageHash]);
  return result !== undefined;
}

export function getTagsForFiles(filepaths: string[]): Record<string, Tag[]> {
  if (!filepaths.length) {
    return {};
  }
  
  const conn = getDb();
  const placeholders = filepaths.map(() => "?").join(",");
  const query = `SELECT filepath, tag, confidence FROM image_tags WHERE filepath IN (${placeholders}) ORDER BY confidence DESC`;
  
  const rows = conn.query<[string, string, number]>(query, filepaths);
  
  const tagsMap: Record<string, Tag[]> = Object.fromEntries(filepaths.map(path => [path, []]));

  for (const [filepath, tag, confidence] of rows) {
    tagsMap[filepath].push({ tag, confidence });
  }
  
  return tagsMap;
}

export function getAllTags(): { tag: string; count: number }[] {
  const conn = getDb();
  const rows = conn.query<[string, number]>(`
    SELECT tag, COUNT(id) as tag_count
    FROM image_tags
    GROUP BY tag
    ORDER BY tag_count DESC, tag ASC
  `);
  return rows.map(([tag, count]) => ({ tag, count }));
}

export function findFilesByTags(tags: string[]): string[] {
    if (!tags || tags.length === 0) {
        return [];
    }
    const conn = getDb();

    let query = "SELECT filepath FROM image_tags WHERE tag = ?";
    for (let i = 1; i < tags.length; i++) {
        query += " INTERSECT SELECT filepath FROM image_tags WHERE tag = ?";
    }

    const rows = conn.query<[string]>(query, tags);
    return rows.map(row => row[0]);
}

export function deleteAllTags(): void {
    getDb().query("DELETE FROM image_tags");
}

export function clearAllProcessedImages(): void {
    getDb().query("DELETE FROM processed_images");
}

export function getAllImageFilepathsFromDb(): Set<string> {
    const rows = getDb().query<[string]>("SELECT DISTINCT filepath FROM image_tags");
    return new Set(rows.map(row => row[0]));
}
