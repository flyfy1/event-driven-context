PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA recursive_triggers = ON;

CREATE TABLE IF NOT EXISTS users (
 id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, email TEXT, password_hash BLOB NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
 hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL, timezone TEXT NOT NULL DEFAULT 'UTC', owner_user_id TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
 project_id TEXT NOT NULL REFERENCES projects(id), user_id TEXT NOT NULL REFERENCES users(id), PRIMARY KEY(project_id,user_id)
);
CREATE TABLE IF NOT EXISTS project_owners (
 project_id TEXT NOT NULL REFERENCES projects(id), user_id TEXT NOT NULL REFERENCES users(id), PRIMARY KEY(project_id,user_id),
 FOREIGN KEY(project_id,user_id) REFERENCES members(project_id,user_id)
);
CREATE TABLE IF NOT EXISTS oauth_clients (
 client_id TEXT PRIMARY KEY, client_name TEXT NOT NULL, redirect_uris TEXT NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_requests (
 id_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL REFERENCES oauth_clients(client_id),
 redirect_uri TEXT NOT NULL, state TEXT NOT NULL, code_challenge TEXT NOT NULL,
 scope TEXT NOT NULL, resource TEXT NOT NULL, csrf_hash TEXT NOT NULL,
 expires_at INTEGER NOT NULL, used_at INTEGER
);
CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
 code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL REFERENCES oauth_clients(client_id),
 user_id TEXT NOT NULL REFERENCES users(id), redirect_uri TEXT NOT NULL,
 code_challenge TEXT NOT NULL, scope TEXT NOT NULL, resource TEXT NOT NULL,
 expires_at INTEGER NOT NULL, used_at INTEGER
);
CREATE TABLE IF NOT EXISTS oauth_access_tokens (
 hash TEXT PRIMARY KEY, client_id TEXT NOT NULL REFERENCES oauth_clients(client_id),
 user_id TEXT NOT NULL REFERENCES users(id), scope TEXT NOT NULL, resource TEXT NOT NULL,
 expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL
);
