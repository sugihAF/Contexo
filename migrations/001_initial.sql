-- CtxHub Server PostgreSQL Schema

CREATE TABLE IF NOT EXISTS orgs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS repos (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS commits (
    id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    feature TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    data_json JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (repo_id, id)
);

CREATE INDEX IF NOT EXISTS idx_commits_feature ON commits(repo_id, feature);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    event_count INTEGER NOT NULL DEFAULT 0,
    feature TEXT NOT NULL DEFAULT '',
    s3_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repo_id, id)
);

CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ
);
