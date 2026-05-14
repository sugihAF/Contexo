package sqlite

const migrateSQL = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	source TEXT NOT NULL DEFAULT '',
	repo_id TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL,
	ended_at TEXT,
	event_count INTEGER NOT NULL DEFAULT 0,
	feature TEXT NOT NULL DEFAULT '',
	jsonl_path TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS events (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	type TEXT NOT NULL,
	turn INTEGER NOT NULL DEFAULT 0,
	ts TEXT NOT NULL,
	content_text TEXT NOT NULL DEFAULT '',
	blob_id TEXT NOT NULL DEFAULT '',
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id);
CREATE INDEX IF NOT EXISTS idx_events_turn ON events(session_id, turn);

CREATE TABLE IF NOT EXISTS commits (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	feature TEXT NOT NULL DEFAULT '',
	author TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	parent_id TEXT NOT NULL DEFAULT '',
	data_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_commits_feature ON commits(feature);

CREATE TABLE IF NOT EXISTS commit_files (
	commit_id TEXT NOT NULL,
	path TEXT NOT NULL,
	action TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (commit_id, path),
	FOREIGN KEY (commit_id) REFERENCES commits(id)
);

CREATE TABLE IF NOT EXISTS commit_symbols (
	commit_id TEXT NOT NULL,
	symbol_key TEXT NOT NULL,
	PRIMARY KEY (commit_id, symbol_key),
	FOREIGN KEY (commit_id) REFERENCES commits(id)
);

CREATE TABLE IF NOT EXISTS symbol_index (
	symbol_key TEXT NOT NULL,
	commit_id TEXT NOT NULL,
	PRIMARY KEY (symbol_key, commit_id)
);

CREATE TABLE IF NOT EXISTS git_links (
	git_sha TEXT NOT NULL,
	commit_id TEXT NOT NULL,
	PRIMARY KEY (git_sha, commit_id)
);

CREATE INDEX IF NOT EXISTS idx_git_links_commit ON git_links(commit_id);

CREATE TABLE IF NOT EXISTS sync_status (
	item_type TEXT NOT NULL,
	item_id TEXT NOT NULL,
	synced_at TEXT,
	PRIMARY KEY (item_type, item_id)
);

CREATE TABLE IF NOT EXISTS features (
	repo_id TEXT NOT NULL,
	feature TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	data_json TEXT NOT NULL DEFAULT '{}',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (repo_id, feature)
);

CREATE TABLE IF NOT EXISTS activity_log (
	id TEXT PRIMARY KEY,
	repo_id TEXT NOT NULL,
	feature TEXT NOT NULL,
	type TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	commit_id TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL DEFAULT '',
	ts TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_activity_feature ON activity_log(repo_id, feature, ts);
`
