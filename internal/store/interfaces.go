package store

import (
	"context"

	"github.com/sugihAF/contexo/internal/schema"
)

// SessionFilter controls session listing queries.
type SessionFilter struct {
	Source  string
	Feature string
	RepoID  string
	Limit   int
	Offset  int
}

// CommitFilter controls commit listing queries.
type CommitFilter struct {
	Feature string
	RepoID  string
	Author  string
	Limit   int
	Offset  int
}

// EventStore manages session event persistence and retrieval.
type EventStore interface {
	AppendEvent(ctx context.Context, event *schema.SessionEvent) error
	GetSession(ctx context.Context, sessionID string) (*schema.SessionMeta, error)
	ListSessions(ctx context.Context, filter SessionFilter) ([]*schema.SessionMeta, error)
	ReadSlice(ctx context.Context, sessionID string, fromTurn, toTurn int) ([]*schema.SessionEvent, error)
}

// CommitStore manages context commit persistence and retrieval.
type CommitStore interface {
	CreateCommit(ctx context.Context, commit *schema.ContextCommit) error
	GetCommit(ctx context.Context, commitID string) (*schema.ContextCommit, error)
	ListCommits(ctx context.Context, filter CommitFilter) ([]*schema.ContextCommit, error)
	LinkGit(ctx context.Context, gitSHA, commitID string) error
	GetBySymbol(ctx context.Context, symbolKey string) ([]*schema.ContextCommit, error)
}

// BlobStore manages content-addressed blob storage.
type BlobStore interface {
	Put(ctx context.Context, data []byte) (hash string, err error)
	Get(ctx context.Context, hash string) ([]byte, error)
	Exists(ctx context.Context, hash string) (bool, error)
}

// BlobMeta holds metadata about a stored blob.
type BlobMeta struct {
	Hash      string
	Size      int64
	CreatedAt int64
}

// BlobMetaStore extends BlobStore with metadata queries.
type BlobMetaStore interface {
	BlobStore
	Meta(ctx context.Context, hash string) (*BlobMeta, error)
}

// FeatureStore manages feature overview and activity data.
type FeatureStore interface {
	GetOverview(ctx context.Context, repoID, feature string) (*schema.FeatureOverview, error)
	PutOverview(ctx context.Context, overview *schema.FeatureOverview) error
	AppendActivity(ctx context.Context, entry *schema.ActivityEntry) error
	ListActivity(ctx context.Context, repoID, feature string, limit int) ([]*schema.ActivityEntry, error)
}
