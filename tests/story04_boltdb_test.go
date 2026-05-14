package tests

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	boltdbstore "github.com/sugihAF/contexo/internal/store/boltdb"
)

func openTestBlobStore(t *testing.T) *boltdbstore.BlobStore {
	t.Helper()
	dir := t.TempDir()
	bs, err := boltdbstore.New(
		filepath.Join(dir, "blobs.db"),
		filepath.Join(dir, "blobs"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { bs.Close() })
	return bs
}

func TestStory04_PutComputesSHA256(t *testing.T) {
	bs := openTestBlobStore(t)
	ctx := context.Background()

	data := []byte("hello world")
	hash, err := bs.Put(ctx, data)
	require.NoError(t, err)
	// SHA-256 of "hello world"
	assert.Equal(t, "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", hash)
}

func TestStory04_GetReturnsData(t *testing.T) {
	bs := openTestBlobStore(t)
	ctx := context.Background()

	data := []byte("test data for blob store")
	hash, err := bs.Put(ctx, data)
	require.NoError(t, err)

	got, err := bs.Get(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestStory04_ExistsWorks(t *testing.T) {
	bs := openTestBlobStore(t)
	ctx := context.Background()

	exists, err := bs.Exists(ctx, "nonexistent")
	require.NoError(t, err)
	assert.False(t, exists)

	hash, err := bs.Put(ctx, []byte("data"))
	require.NoError(t, err)

	exists, err = bs.Exists(ctx, hash)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestStory04_MetaReturnsSizeAndCreatedAt(t *testing.T) {
	bs := openTestBlobStore(t)
	ctx := context.Background()

	data := []byte("metadata test")
	hash, err := bs.Put(ctx, data)
	require.NoError(t, err)

	meta, err := bs.Meta(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, int64(len(data)), meta.Size)
	assert.Greater(t, meta.CreatedAt, int64(0))
}

func TestStory04_DedupSameContent(t *testing.T) {
	bs := openTestBlobStore(t)
	ctx := context.Background()

	data := []byte("same content twice")
	hash1, err := bs.Put(ctx, data)
	require.NoError(t, err)

	hash2, err := bs.Put(ctx, data)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2)

	got, err := bs.Get(ctx, hash1)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}
