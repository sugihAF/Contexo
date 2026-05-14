package boltdb

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sugihAF/contexo/internal/store"
	bolt "go.etcd.io/bbolt"
)

var blobBucket = []byte("blobs")

// BlobStore implements store.BlobMetaStore using BoltDB for metadata
// and the filesystem for blob data.
type BlobStore struct {
	db      *bolt.DB
	blobDir string
}

// New creates a BlobStore with BoltDB at dbPath and blobs in blobDir.
func New(dbPath, blobDir string) (*BlobStore, error) {
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		return nil, fmt.Errorf("boltdb: create blob dir: %w", err)
	}

	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("boltdb: open %s: %w", dbPath, err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(blobBucket)
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("boltdb: create bucket: %w", err)
	}

	return &BlobStore{db: db, blobDir: blobDir}, nil
}

// Put stores data and returns its SHA-256 hash. Deduplicates by hash.
func (bs *BlobStore) Put(_ context.Context, data []byte) (string, error) {
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])

	blobPath := bs.blobPath(hash)

	// Check if blob file already exists (dedup)
	if _, err := os.Stat(blobPath); err == nil {
		return hash, nil
	}

	// Write blob file
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		return "", fmt.Errorf("boltdb: create blob subdir: %w", err)
	}
	if err := os.WriteFile(blobPath, data, 0o644); err != nil {
		return "", fmt.Errorf("boltdb: write blob: %w", err)
	}

	// Store metadata in BoltDB
	if err := bs.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(blobBucket)
		// Store: 8 bytes size + 8 bytes created_at
		val := make([]byte, 16)
		binary.BigEndian.PutUint64(val[0:8], uint64(len(data)))
		binary.BigEndian.PutUint64(val[8:16], uint64(time.Now().UnixNano()))
		return b.Put([]byte(hash), val)
	}); err != nil {
		return "", fmt.Errorf("boltdb: store meta: %w", err)
	}

	return hash, nil
}

// Get retrieves blob data by hash.
func (bs *BlobStore) Get(_ context.Context, hash string) ([]byte, error) {
	blobPath := bs.blobPath(hash)
	data, err := os.ReadFile(blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("boltdb: blob not found: %s", hash)
		}
		return nil, fmt.Errorf("boltdb: read blob: %w", err)
	}
	return data, nil
}

// Exists checks if a blob exists.
func (bs *BlobStore) Exists(_ context.Context, hash string) (bool, error) {
	_, err := os.Stat(bs.blobPath(hash))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Meta returns metadata for a blob.
func (bs *BlobStore) Meta(_ context.Context, hash string) (*store.BlobMeta, error) {
	var meta store.BlobMeta
	meta.Hash = hash

	err := bs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(blobBucket)
		val := b.Get([]byte(hash))
		if val == nil {
			return fmt.Errorf("boltdb: meta not found: %s", hash)
		}
		if len(val) >= 16 {
			meta.Size = int64(binary.BigEndian.Uint64(val[0:8]))
			meta.CreatedAt = int64(binary.BigEndian.Uint64(val[8:16]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

// Close closes the BoltDB database.
func (bs *BlobStore) Close() error {
	return bs.db.Close()
}

func (bs *BlobStore) blobPath(hash string) string {
	// Use first 2 chars as subdir for sharding
	if len(hash) >= 2 {
		return filepath.Join(bs.blobDir, hash[:2], hash)
	}
	return filepath.Join(bs.blobDir, hash)
}
