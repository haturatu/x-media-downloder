
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
    if os.path.exists(DATABASE_PATH):
        return
        
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("""
        CREATE TABLE image_tags (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            filepath TEXT NOT NULL,
            tag TEXT NOT NULL,
            confidence REAL,
            UNIQUE(filepath, tag)
        );
    """)
    conn.commit()
    conn.close()
    print("Database initialized.")

def add_tags_for_file(filepath, tags):
    """
    Adds a list of tags for a specific file to the database.
    Each tag is a dictionary with 'tag' and 'confidence'.
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

def get_tags_for_files(filepaths):
    """
    Retrieves all tags for a given list of filepaths.
    Returns a dictionary mapping each filepath to a list of its tags.
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

def clear_all_tags():
    """Deletes all records from the image_tags table."""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("DELETE FROM image_tags")
    conn.commit()
    conn.close()
    print("All tags have been cleared from the database.")

def find_files_by_tags(tags):
    """
    Finds all filepaths that have all of the specified tags.
    `tags` should be a list of tag strings.
    """
    if not tags:
        return []

    conn = get_db_connection()
    cursor = conn.cursor()

    # Base query to find files matching the first tag
    query = "SELECT filepath FROM image_tags WHERE tag = ?"
    
    # Intersect with files matching subsequent tags
    for i in range(1, len(tags)):
        query += " INTERSECT SELECT filepath FROM image_tags WHERE tag = ?"
        
    cursor.execute(query, tags)
    rows = cursor.fetchall()
    conn.close()
    
    return [row['filepath'] for row in rows]
