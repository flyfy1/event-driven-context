CREATE TABLE IF NOT EXISTS hub_agents (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), name TEXT NOT NULL,
 token_hash TEXT NOT NULL UNIQUE, verification_code TEXT NOT NULL,
 status TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS hub_agents_owner ON hub_agents(owner_id);
CREATE TABLE IF NOT EXISTS hub_connections (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id), provider_id TEXT NOT NULL,
 account_id TEXT NOT NULL, display_name TEXT NOT NULL, status TEXT NOT NULL,
 credential BLOB NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
 UNIQUE(owner_id,provider_id,account_id)
);
CREATE TABLE IF NOT EXISTS hub_requests (
 id TEXT PRIMARY KEY, agent_id TEXT NOT NULL REFERENCES hub_agents(id),
 connection_id TEXT NOT NULL REFERENCES hub_connections(id), operation TEXT NOT NULL,
 reason TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS hub_requests_agent ON hub_requests(agent_id);
CREATE TABLE IF NOT EXISTS hub_request_constraints (
 request_id TEXT PRIMARY KEY REFERENCES hub_requests(id),
 constraints_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS hub_webhook_receipts (
 id TEXT PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES hub_connections(id),
 sha256 TEXT NOT NULL, encrypted_body BLOB NOT NULL, recorded_at INTEGER NOT NULL,
 UNIQUE(connection_id,sha256)
);
CREATE TABLE IF NOT EXISTS hub_webhook_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 connection_id TEXT NOT NULL REFERENCES hub_connections(id),
 receipt_id TEXT NOT NULL REFERENCES hub_webhook_receipts(id),
 event_key TEXT NOT NULL, kind TEXT NOT NULL, encrypted_body BLOB NOT NULL,
 recorded_at INTEGER NOT NULL, UNIQUE(connection_id,event_key)
);
CREATE INDEX IF NOT EXISTS hub_webhook_events_connection ON hub_webhook_events(connection_id,sequence);
