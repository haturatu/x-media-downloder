package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func openStore(path string) (*store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o664)
	if err != nil {
		return nil, fmt.Errorf("failed to open db file %s for read/write: %w", path, err)
	}
	_ = f.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sql open failed for %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE;`); err != nil {
		return nil, fmt.Errorf("set journal mode failed for %s: %w", path, err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("set busy timeout failed for %s: %w", path, err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS image_tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			filepath TEXT NOT NULL,
			tag TEXT NOT NULL,
			confidence REAL,
			UNIQUE(filepath, tag)
		);
	`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS processed_images (
			image_hash TEXT PRIMARY KEY
		);
	`); err != nil {
		return nil, err
	}
	return &store{db: db}, nil
}

func isRetryableSQLiteError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database is busy") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "unable to open database file")
}

func withSQLiteRetry(op func() error) error {
	var err error
	backoff := 50 * time.Millisecond
	for i := 0; i < 4; i++ {
		err = op()
		if err == nil {
			return nil
		}
		if !isRetryableSQLiteError(err) {
			return err
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return err
}

func (s *store) Close() error {
	return s.db.Close()
}

func (s *store) IsImageProcessed(hash string) (bool, error) {
	var found bool
	err := withSQLiteRetry(func() error {
		var x int
		err := s.db.QueryRow(`SELECT 1 FROM processed_images WHERE image_hash = ?`, hash).Scan(&x)
		if errors.Is(err, sql.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	return found, err
}

func (s *store) MarkImageProcessed(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`INSERT OR IGNORE INTO processed_images (image_hash) VALUES (?)`, hash)
		return err
	})
}

func (s *store) AddTags(filepath string, tags map[string]float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.Prepare(`INSERT OR IGNORE INTO image_tags (filepath, tag, confidence) VALUES (?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for tag, conf := range tags {
			if _, err := stmt.Exec(filepath, tag, conf); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

func (s *store) DeleteAllTags() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM image_tags`)
		return err
	})
}

func (s *store) ClearProcessedImages() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM processed_images`)
		return err
	})
}

func (s *store) GetAllTaggedFilepaths() (map[string]struct{}, error) {
	result := make(map[string]struct{})
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(`SELECT DISTINCT filepath FROM image_tags`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			result[p] = struct{}{}
		}
		return rows.Err()
	})
	return result, err
}

func (s *store) GetAllProcessedHashes() ([]string, error) {
	items := make([]string, 0)
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(`SELECT image_hash FROM processed_images`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				return err
			}
			items = append(items, h)
		}
		return rows.Err()
	})
	return items, err
}

func (s *store) DeleteProcessedHashes(hashes []string) (int, error) {
	if len(hashes) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	totalDeleted := 0
	const chunkSize = 500
	for start := 0; start < len(hashes); start += chunkSize {
		end := start + chunkSize
		if end > len(hashes) {
			end = len(hashes)
		}
		chunk := hashes[start:end]

		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		query := fmt.Sprintf("DELETE FROM processed_images WHERE image_hash IN (%s)", placeholders)
		args := make([]any, 0, len(chunk))
		for _, h := range chunk {
			args = append(args, h)
		}

		var deleted int64
		err := withSQLiteRetry(func() error {
			res, err := s.db.Exec(query, args...)
			if err != nil {
				return err
			}
			deleted, _ = res.RowsAffected()
			return nil
		})
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += int(deleted)
	}
	return totalDeleted, nil
}

func (s *store) GetTagsForFiles(filepaths []string) (map[string][]imageTag, error) {
	result := make(map[string][]imageTag, len(filepaths))
	for _, p := range filepaths {
		result[p] = []imageTag{}
	}
	if len(filepaths) == 0 {
		return result, nil
	}

	const chunkSize = 500
	for start := 0; start < len(filepaths); start += chunkSize {
		end := start + chunkSize
		if end > len(filepaths) {
			end = len(filepaths)
		}
		chunk := filepaths[start:end]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		query := fmt.Sprintf(
			"SELECT filepath, tag, confidence FROM image_tags WHERE filepath IN (%s) ORDER BY confidence DESC",
			placeholders,
		)
		args := make([]any, 0, len(chunk))
		for _, p := range chunk {
			args = append(args, p)
		}

		err := withSQLiteRetry(func() error {
			rows, err := s.db.Query(query, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var filepathVal string
				var tag string
				var confidence float64
				if err := rows.Scan(&filepathVal, &tag, &confidence); err != nil {
					return err
				}
				result[filepathVal] = append(result[filepathVal], imageTag{Tag: tag, Confidence: confidence})
			}
			return rows.Err()
		})
		if err != nil {
			return result, err
		}
	}

	return result, nil
}

func (s *store) GetAllTags() ([]map[string]any, error) {
	items := make([]map[string]any, 0)
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(`
			SELECT tag, COUNT(id) as tag_count
			FROM image_tags
			GROUP BY tag
			ORDER BY tag_count DESC, tag ASC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tag string
			var count int
			if err := rows.Scan(&tag, &count); err != nil {
				return err
			}
			items = append(items, map[string]any{"tag": tag, "count": count})
		}
		return rows.Err()
	})
	return items, err
}

func (s *store) FindFilesByTagPatterns(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return []string{}, nil
	}
	query := "SELECT filepath FROM image_tags WHERE LOWER(tag) LIKE ?"
	for i := 1; i < len(tags); i++ {
		query += " INTERSECT SELECT filepath FROM image_tags WHERE LOWER(tag) LIKE ?"
	}
	args := make([]any, 0, len(tags))
	for _, tag := range tags {
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(tag))+"%")
	}
	items := make([]string, 0)
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var filepathVal string
			if err := rows.Scan(&filepathVal); err != nil {
				return err
			}
			items = append(items, filepathVal)
		}
		return rows.Err()
	})
	return items, err
}

func (s *store) FindFilesByExactTag(tag string) ([]string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return []string{}, nil
	}
	items := make([]string, 0)
	err := withSQLiteRetry(func() error {
		rows, err := s.db.Query(
			`SELECT DISTINCT filepath FROM image_tags WHERE LOWER(tag) = LOWER(?)`,
			tag,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var filepathVal string
			if err := rows.Scan(&filepathVal); err != nil {
				return err
			}
			items = append(items, filepathVal)
		}
		return rows.Err()
	})
	return items, err
}

func (s *store) DeleteTag(tag string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var affected int64
	err := withSQLiteRetry(func() error {
		result, err := s.db.Exec(`DELETE FROM image_tags WHERE tag = ?`, tag)
		if err != nil {
			return err
		}
		affected, _ = result.RowsAffected()
		return nil
	})
	return int(affected), err
}

func (s *store) DeleteTagsForFile(filepathVal string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM image_tags WHERE filepath = ?`, filepathVal)
		return err
	})
}

func (s *store) DeleteTagsForUser(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSQLiteRetry(func() error {
		_, err := s.db.Exec(`DELETE FROM image_tags WHERE filepath LIKE ?`, username+"/%")
		return err
	})
}
