package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sitewatch/sitewatch/internal/snapshot"
)

type Monitor struct {
	ID            int64         `json:"id"`
	Name          string        `json:"name"`
	URL           string        `json:"url"`
	NormalizedURL string        `json:"normalized_url"`
	Interval      time.Duration `json:"interval"`
	Crawl         bool          `json:"crawl"`
	Depth         int           `json:"depth"`
	MaxPages      int           `json:"max_pages"`
	Enabled       bool          `json:"enabled"`
	AllowPrivate  bool          `json:"allow_private"`
	Webhook       string        `json:"webhook,omitempty"`
	Retention     int           `json:"retention"`
	ETag          string        `json:"etag,omitempty"`
	LastModified  string        `json:"last_modified,omitempty"`
	LastCheckedAt *time.Time    `json:"last_checked_at,omitempty"`
	LastStatus    string        `json:"last_status"`
	LastError     string        `json:"last_error,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
}

type DB struct{ db *sql.DB }

func Open(path string) (*DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &DB{db: db}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *DB) Close() error { return s.db.Close() }
func (s *DB) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS monitors (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL, url TEXT NOT NULL, normalized_url TEXT NOT NULL UNIQUE,
 interval_seconds INTEGER NOT NULL, crawl INTEGER NOT NULL DEFAULT 0, depth INTEGER NOT NULL DEFAULT 1,
 max_pages INTEGER NOT NULL DEFAULT 50, enabled INTEGER NOT NULL DEFAULT 1, allow_private INTEGER NOT NULL DEFAULT 0,
 webhook TEXT NOT NULL DEFAULT '', retention INTEGER NOT NULL DEFAULT 30, etag TEXT NOT NULL DEFAULT '',
 last_modified TEXT NOT NULL DEFAULT '', last_checked_at TEXT, last_status TEXT NOT NULL DEFAULT 'new',
 last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS snapshots (
 id INTEGER PRIMARY KEY, monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
 url TEXT NOT NULL, http_status INTEGER NOT NULL, title TEXT NOT NULL, content_hash TEXT NOT NULL,
 visible_text_hash TEXT NOT NULL, structured_json BLOB NOT NULL, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS snapshots_monitor_created ON snapshots(monitor_id,created_at DESC);
CREATE TABLE IF NOT EXISTS changes (
 id INTEGER PRIMARY KEY, monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
 snapshot_from INTEGER REFERENCES snapshots(id) ON DELETE CASCADE, snapshot_to INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
 change_type TEXT NOT NULL, entity_type TEXT NOT NULL, category TEXT NOT NULL, old_value TEXT NOT NULL,
 new_value TEXT NOT NULL, context TEXT NOT NULL, significance REAL NOT NULL, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS changes_monitor_created ON changes(monitor_id,created_at DESC);`)
	return err
}

func (s *DB) AddMonitor(ctx context.Context, m Monitor) (Monitor, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r, err := s.db.ExecContext(ctx, `INSERT INTO monitors(name,url,normalized_url,interval_seconds,crawl,depth,max_pages,allow_private,webhook,retention,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, m.Name, m.URL, m.NormalizedURL, int64(m.Interval/time.Second), m.Crawl, m.Depth, m.MaxPages, m.AllowPrivate, m.Webhook, m.Retention, now, now)
	if err != nil {
		return m, fmt.Errorf("add monitor: %w", err)
	}
	m.ID, _ = r.LastInsertId()
	m.Enabled = true
	m.LastStatus = "new"
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, now)
	return m, nil
}
func (s *DB) ListMonitors(ctx context.Context) ([]Monitor, error) {
	rows, err := s.db.QueryContext(ctx, monitorSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *DB) GetMonitor(ctx context.Context, ref string) (Monitor, error) {
	q := monitorSelect + ` WHERE normalized_url=? OR url=?`
	args := []any{ref, ref}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		q = monitorSelect + ` WHERE id=?`
		args = []any{id}
	}
	m, err := scanMonitor(s.db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return m, errors.New("monitor not found")
	}
	return m, err
}
func (s *DB) RemoveMonitor(ctx context.Context, id int64) error {
	r, err := s.db.ExecContext(ctx, `DELETE FROM monitors WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return errors.New("monitor not found")
	}
	return nil
}

const monitorSelect = `SELECT id,name,url,normalized_url,interval_seconds,crawl,depth,max_pages,enabled,allow_private,webhook,retention,etag,last_modified,last_checked_at,last_status,last_error,created_at FROM monitors`

type scanner interface{ Scan(...any) error }

func scanMonitor(row scanner) (Monitor, error) {
	var m Monitor
	var secs int64
	var checked sql.NullString
	var created string
	err := row.Scan(&m.ID, &m.Name, &m.URL, &m.NormalizedURL, &secs, &m.Crawl, &m.Depth, &m.MaxPages, &m.Enabled, &m.AllowPrivate, &m.Webhook, &m.Retention, &m.ETag, &m.LastModified, &checked, &m.LastStatus, &m.LastError, &created)
	m.Interval = time.Duration(secs) * time.Second
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if checked.Valid {
		t, _ := time.Parse(time.RFC3339Nano, checked.String)
		m.LastCheckedAt = &t
	}
	return m, err
}

func (s *DB) UpdateCheck(ctx context.Context, id int64, etag, lastModified, status, lastErr string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE monitors SET etag=?,last_modified=?,last_checked_at=?,last_status=?,last_error=?,updated_at=? WHERE id=?`, etag, lastModified, time.Now().UTC().Format(time.RFC3339Nano), status, lastErr, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}
func (s *DB) StoreSnapshot(ctx context.Context, monitorID int64, p snapshot.Page, changes []snapshot.Change, retention int) (snapshot.Page, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var from sql.NullInt64
	_ = tx.QueryRowContext(ctx, `SELECT id FROM snapshots WHERE monitor_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, monitorID).Scan(&from)
	r, err := tx.ExecContext(ctx, `INSERT INTO snapshots(monitor_id,url,http_status,title,content_hash,visible_text_hash,structured_json,created_at) VALUES(?,?,?,?,?,?,?,?)`, monitorID, p.URL, p.HTTPStatus, p.Title, p.Fingerprints.HTML, p.Fingerprints.Visible, b, p.FetchedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return p, err
	}
	p.ID, _ = r.LastInsertId()
	for _, c := range changes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO changes(monitor_id,snapshot_from,snapshot_to,change_type,entity_type,category,old_value,new_value,context,significance,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, monitorID, from, p.ID, c.Type, c.Entity, c.Category, c.OldValue, c.NewValue, c.Context, c.Score, p.FetchedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return p, err
		}
	}
	if retention > 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM snapshots WHERE monitor_id=? AND id NOT IN (SELECT id FROM snapshots WHERE monitor_id=? ORDER BY created_at DESC,id DESC LIMIT ?)`, monitorID, monitorID, retention)
		if err != nil {
			return p, err
		}
	}
	return p, tx.Commit()
}
func (s *DB) History(ctx context.Context, monitorID int64, limit int) ([]snapshot.Page, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,structured_json FROM snapshots WHERE monitor_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []snapshot.Page
	for rows.Next() {
		var id int64
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		var p snapshot.Page
		if err = json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		p.ID = id
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *DB) Latest(ctx context.Context, monitorID int64) (snapshot.Page, error) {
	h, err := s.History(ctx, monitorID, 1)
	if err != nil {
		return snapshot.Page{}, err
	}
	if len(h) == 0 {
		return snapshot.Page{}, sql.ErrNoRows
	}
	return h[0], nil
}
func (s *DB) LatestChanges(ctx context.Context, monitorID int64) ([]snapshot.Change, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,change_type,entity_type,category,old_value,new_value,context,significance FROM changes WHERE snapshot_to=(SELECT id FROM snapshots WHERE monitor_id=? ORDER BY created_at DESC,id DESC LIMIT 1) ORDER BY significance DESC,id`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []snapshot.Change
	for rows.Next() {
		var c snapshot.Change
		if err := rows.Scan(&c.ID, &c.Type, &c.Entity, &c.Category, &c.OldValue, &c.NewValue, &c.Context, &c.Score); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *DB) SnapshotPair(ctx context.Context, monitorID int64) (snapshot.Page, snapshot.Page, error) {
	h, err := s.History(ctx, monitorID, 2)
	if err != nil || len(h) < 2 {
		return snapshot.Page{}, snapshot.Page{}, errors.New("at least two snapshots are required")
	}
	return h[1], h[0], nil
}

func IsUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
