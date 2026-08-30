-- Embedded in internal/storage so the single binary needs no external files.
CREATE TABLE monitors (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, url TEXT NOT NULL,
  normalized_url TEXT NOT NULL UNIQUE, interval_seconds INTEGER NOT NULL,
  crawl INTEGER NOT NULL DEFAULT 0, depth INTEGER NOT NULL DEFAULT 1,
  max_pages INTEGER NOT NULL DEFAULT 50, enabled INTEGER NOT NULL DEFAULT 1,
  allow_private INTEGER NOT NULL DEFAULT 0, webhook TEXT NOT NULL DEFAULT '',
  retention INTEGER NOT NULL DEFAULT 30, etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '', last_checked_at TEXT,
  last_status TEXT NOT NULL DEFAULT 'new', last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE snapshots (
  id INTEGER PRIMARY KEY,
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  url TEXT NOT NULL, http_status INTEGER NOT NULL, title TEXT NOT NULL,
  content_hash TEXT NOT NULL, visible_text_hash TEXT NOT NULL,
  structured_json BLOB NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX snapshots_monitor_created ON snapshots(monitor_id, created_at DESC);
CREATE TABLE changes (
  id INTEGER PRIMARY KEY,
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  snapshot_from INTEGER REFERENCES snapshots(id) ON DELETE CASCADE,
  snapshot_to INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  change_type TEXT NOT NULL, entity_type TEXT NOT NULL, category TEXT NOT NULL,
  old_value TEXT NOT NULL, new_value TEXT NOT NULL, context TEXT NOT NULL,
  significance REAL NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX changes_monitor_created ON changes(monitor_id, created_at DESC);
