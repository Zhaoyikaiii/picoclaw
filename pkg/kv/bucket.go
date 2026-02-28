// PicoClaw - Ultra-lightweight personal AI agent
// Generic Key-Value storage abstraction
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package kv provides a generic key-value storage abstraction.
// It supports multiple backends (NATS JetStream, etcd, Consul, etc.)
// with CAS (Compare-And-Swap) operations and change watching.
package kv

import "time"

// BucketSpec defines the configuration for a bucket.
type BucketSpec struct {
	Name         string        // Bucket name
	Description  string        // Human-readable description
	TTL          time.Duration // TTL for entries
	MaxValueSize int32         // Maximum value size
	Storage      StorageType   // Storage backend
}

// StorageType defines the storage backend for a bucket.
type StorageType string

const (
	StorageFile   StorageType = "file"
	StorageMemory StorageType = "memory"
)

// BucketEntry represents a key-value entry in a bucket.
type BucketEntry struct {
	Key       string         // The key
	Value     []byte         // The value
	Revision  uint64         // Revision number (for CAS)
	Operation EntryOperation // The operation that triggered this entry
}

// EntryOperation represents the type of operation on an entry.
type EntryOperation string

const (
	EntryOperationPut    EntryOperation = "put"
	EntryOperationDelete EntryOperation = "delete"
	EntryOperationPurge  EntryOperation = "purge"
)

// BucketWatcher watches for changes in a bucket.
type BucketWatcher interface {
	// Updates returns a channel of bucket entry updates.
	// The channel is closed when Stop() is called.
	Updates() <-chan *BucketEntry

	// Stop stops watching and closes the updates channel.
	Stop()
}

// Bucket provides key-value storage with watch capabilities.
//
// This interface abstracts KV operations for different storage backends.
// Implementations can use NATS JetStream KV, etcd, Consul, Redis, etc.
type Bucket interface {
	// Name returns the bucket name.
	Name() string

	// Get retrieves a value by key.
	// Returns nil if key does not exist.
	Get(key string) (*BucketEntry, error)

	// Put stores a value under the given key (creates or updates).
	Put(key string, value []byte) error

	// Create stores a value only if the key does not exist.
	// Returns the revision number of the new entry.
	// Returns an error if the key already exists.
	Create(key string, value []byte) (uint64, error)

	// Update updates a value with CAS (Compare-And-Swap) semantics.
	// The operation only succeeds if the entry's revision matches the expected revision.
	// Returns the new revision number.
	// Returns an error if the revision doesn't match (CAS failure).
	Update(key string, value []byte, revision uint64) (uint64, error)

	// Delete removes a key.
	Delete(key string) error

	// Keys returns all keys in the bucket.
	Keys() ([]string, error)

	// Watch starts watching for changes in the bucket.
	// If keyPrefix is provided, only watch keys with that prefix.
	// Returns a BucketWatcher that can be stopped.
	Watch(keyPrefix string) (BucketWatcher, error)

	// WatchAll watches all keys in the bucket.
	WatchAll() (BucketWatcher, error)

	// Close closes the bucket and releases resources.
	Close() error
}

// BucketFactory creates buckets of different types.
type BucketFactory interface {
	// CreateBucket creates a new bucket with the given specification.
	CreateBucket(spec BucketSpec) (Bucket, error)

	// GetBucket returns an existing bucket by name.
	GetBucket(name string) (Bucket, error)

	// CreateIfNotExists creates a bucket if it doesn't exist, or returns the existing one.
	CreateIfNotExists(spec BucketSpec) (Bucket, error)

	// Close closes all buckets and releases resources.
	Close() error
}
