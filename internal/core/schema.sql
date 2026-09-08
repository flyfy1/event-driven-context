PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA recursive_triggers = ON;

CREATE TABLE IF NOT EXISTS users (
 id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash BLOB NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
 hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL, owner_user_id TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
 project_id TEXT NOT NULL REFERENCES projects(id), user_id TEXT NOT NULL REFERENCES users(id), PRIMARY KEY(project_id,user_id)
);
CREATE TABLE IF NOT EXISTS files (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), filename TEXT NOT NULL, media_type TEXT NOT NULL,
 size_bytes INTEGER NOT NULL, sha256 TEXT NOT NULL, data BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, project_id TEXT NOT NULL REFERENCES projects(id),
 actor_user_id TEXT NOT NULL REFERENCES users(id), recorded_at TEXT NOT NULL, occurred_at TEXT,
 text_content TEXT, file_id TEXT REFERENCES files(id), metadata TEXT NOT NULL CHECK(json_valid(metadata) AND json_type(metadata)='object'),
 idempotency_key TEXT, request_hash TEXT NOT NULL,
 CHECK ((text_content IS NOT NULL AND file_id IS NULL) OR (text_content IS NULL AND file_id IS NOT NULL)),
 UNIQUE(project_id,actor_user_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS events_project_seq ON events(project_id,seq);
CREATE INDEX IF NOT EXISTS events_project_recorded ON events(project_id,recorded_at);
CREATE INDEX IF NOT EXISTS events_project_occurred ON events(project_id,occurred_at);
-- JSON types and canonical values are indexed once on append. User keys never become SQL paths.
CREATE TABLE IF NOT EXISTS event_metadata (
 event_id TEXT NOT NULL REFERENCES events(id), key TEXT NOT NULL, type TEXT NOT NULL, value TEXT NOT NULL,
 PRIMARY KEY(event_id,key)
);
CREATE INDEX IF NOT EXISTS metadata_lookup ON event_metadata(key,type,value,event_id);
CREATE TRIGGER IF NOT EXISTS events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT,'events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS files_no_update BEFORE UPDATE ON files BEGIN SELECT RAISE(ABORT,'files are append-only'); END;
CREATE TRIGGER IF NOT EXISTS files_no_delete BEFORE DELETE ON files BEGIN SELECT RAISE(ABORT,'files are append-only'); END;
CREATE TRIGGER IF NOT EXISTS metadata_no_update BEFORE UPDATE ON event_metadata BEGIN SELECT RAISE(ABORT,'metadata is append-only'); END;
CREATE TRIGGER IF NOT EXISTS metadata_no_delete BEFORE DELETE ON event_metadata BEGIN SELECT RAISE(ABORT,'metadata is append-only'); END;
