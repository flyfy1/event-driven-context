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
