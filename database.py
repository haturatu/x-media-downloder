import sqlite3
import os

DATABASE_PATH = 'tags.db'

def get_db_connection():
    """Establishes a connection to the SQLite database."""
    conn = sqlite3.connect(DATABASE_PATH)
    conn.row_factory = sqlite3.Row
    return conn

def init_db():
    """Initializes the database and creates the necessary tables."""
    conn = get_db_connection()
    cursor = conn.cursor()
    
    # Main table for tags
    cursor.execute("""
        CREATE TABLE IF NOT EXISTS image_tags (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            filepath TEXT NOT NULL,
            tag TEXT NOT NULL,
            confidence REAL,
            UNIQUE(filepath, tag)
        );
    """)
    
    # Table to track processed images by their content hash
    cursor.execute("""
        CREATE TABLE IF NOT EXISTS processed_images (
            image_hash TEXT PRIMARY KEY
        );
    """)
    
    conn.commit()
    conn.close()
    print("Database initialized.")

def add_tags_for_file(filepath, tags):
    """
    Adds a list of tags for a specific file to the database.
    """
    conn = get_db_connection()
    cursor = conn.cursor()
    for tag_info in tags:
        tag = tag_info.get("tag")
        confidence = tag_info.get("confidence")
        if tag:
            cursor.execute(
                "INSERT OR IGNORE INTO image_tags (filepath, tag, confidence) VALUES (?, ?, ?)",
                (filepath, tag, confidence)
            )
    conn.commit()
    conn.close()

def mark_image_as_processed(image_hash):
    """Adds an image hash to the processed_images table."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("INSERT OR IGNORE INTO processed_images (image_hash) VALUES (?)", (image_hash,))
    conn.commit()
    conn.close()

def is_image_processed(image_hash):
    """Checks if an image hash exists in the processed_images table."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("SELECT 1 FROM processed_images WHERE image_hash = ?", (image_hash,))
    result = cursor.fetchone()
    conn.close()
    return result is not None

def get_tags_for_files(filepaths):
    """
    Retrieves all tags for a given list of filepaths.
    """
    if not filepaths:
        return {}
    
    conn = get_db_connection()
    cursor = conn.cursor()
    
    placeholders = ','.join('?' for _ in filepaths)
    query = f"SELECT filepath, tag, confidence FROM image_tags WHERE filepath IN ({placeholders}) ORDER BY confidence DESC"
    
    cursor.execute(query, filepaths)
    rows = cursor.fetchall()
    conn.close()
    
    tags_map = {path: [] for path in filepaths}
    for row in rows:
        tags_map[row['filepath']].append({
            "tag": row['tag'],
            "confidence": row['confidence']
        })
        
    return tags_map

def get_all_tags():
    """Retrieves all unique tags from the database, sorted by frequency."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("""
        SELECT tag, COUNT(id) as tag_count
        FROM image_tags
        GROUP BY tag
        ORDER BY tag_count DESC, tag ASC
    """)
    rows = cursor.fetchall()
    conn.close()
    return [{"tag": row['tag'], "count": row['tag_count']} for row in rows]

def find_files_by_tags(tags):
    """
    Finds all filepaths that have all of the specified tags.
    """
    if not tags:
        return []

    conn = get_db_connection()
    cursor = conn.cursor()

    query = "SELECT filepath FROM image_tags WHERE tag = ?"
    for i in range(1, len(tags)):
        query += " INTERSECT SELECT filepath FROM image_tags WHERE tag = ?"
        
    cursor.execute(query, tags)
    rows = cursor.fetchall()
    conn.close()
    
    return [row['filepath'] for row in rows]

def delete_all_tags():
    """Deletes all tags from the image_tags table."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("DELETE FROM image_tags")
    conn.commit()
    conn.close()

def clear_all_processed_images():
    """Clears the processed_images table."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("DELETE FROM processed_images")
    conn.commit()
    conn.close()

def get_all_image_filepaths_from_db():
    """Retrieves all unique filepaths from the image_tags table."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("SELECT DISTINCT filepath FROM image_tags")
    rows = cursor.fetchall()
    conn.close()
    return {row['filepath'] for row in rows}

def delete_tags_for_file(filepath):
    """Deletes all tags associated with a specific file."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("DELETE FROM image_tags WHERE filepath = ?", (filepath,))
    conn.commit()
    conn.close()