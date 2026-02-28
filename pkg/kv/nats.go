// PicoClaw - Ultra-lightweight personal AI agent
// Generic Key-Value storage abstraction - NATS JetStream implementation
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package kv

import (
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
)

// NATSBucketFactory implements BucketFactory using NATS JetStream KV.
type NATSBucketFactory struct {
	js      nats.JetStreamContext
	buckets map[string]Bucket
	mu      sync.RWMutex
}

// NewNATSBucketFactory creates a new NATS-backed bucket factory.
func NewNATSBucketFactory(js nats.JetStreamContext) *NATSBucketFactory {
	return &NATSBucketFactory{
		js:      js,
		buckets: make(map[string]Bucket),
	}
}

// CreateBucket creates a new bucket with the given specification.
func (nf *NATSBucketFactory) CreateBucket(spec BucketSpec) (Bucket, error) {
	nf.mu.Lock()
	defer nf.mu.Unlock()

	// Check if already exists
	if _, exists := nf.buckets[spec.Name]; exists {
		return nil, fmt.Errorf("bucket %s already exists", spec.Name)
	}

	storage := nats.FileStorage
	if spec.Storage == StorageMemory {
		storage = nats.MemoryStorage
	}

	kv, err := nf.js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:       spec.Name,
		Description:  spec.Description,
		MaxValueSize: spec.MaxValueSize,
		TTL:          spec.TTL,
		Storage:      storage,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", spec.Name, err)
	}

	bucket := &natsBucket{
		kv:   kv,
		spec: spec,
	}
	nf.buckets[spec.Name] = bucket

	return bucket, nil
}

// GetBucket returns an existing bucket by name.
func (nf *NATSBucketFactory) GetBucket(name string) (Bucket, error) {
	nf.mu.RLock()
	defer nf.mu.RUnlock()

	if b, exists := nf.buckets[name]; exists {
		return b, nil
	}

	// Try to load from NATS
	kv, err := nf.js.KeyValue(name)
	if err != nil {
		return nil, fmt.Errorf("bucket %s not found: %w", name, err)
	}

	bucket := &natsBucket{
		kv:   kv,
		spec: BucketSpec{Name: name},
	}
	nf.buckets[name] = bucket

	return bucket, nil
}

// CreateIfNotExists creates a bucket if it doesn't exist.
func (nf *NATSBucketFactory) CreateIfNotExists(spec BucketSpec) (Bucket, error) {
	b, err := nf.GetBucket(spec.Name)
	if err == nil {
		return b, nil
	}

	return nf.CreateBucket(spec)
}

// Close closes all buckets.
func (nf *NATSBucketFactory) Close() error {
	nf.mu.Lock()
	defer nf.mu.Unlock()

	var lastErr error
	for name := range nf.buckets {
		delete(nf.buckets, name)
	}

	return lastErr
}

// natsBucket implements Bucket using NATS JetStream KV.
type natsBucket struct {
	kv   nats.KeyValue
	spec BucketSpec
}

// Name returns the bucket name.
func (b *natsBucket) Name() string {
	return b.spec.Name
}

// Get retrieves a value by key.
func (b *natsBucket) Get(key string) (*BucketEntry, error) {
	entry, err := b.kv.Get(key)
	if err != nil {
		if err == nats.ErrKeyNotFound {
			return nil, nil
		}
		return nil, err
	}

	return &BucketEntry{
		Key:      entry.Key(),
		Value:    entry.Value(),
		Revision: entry.Revision(),
	}, nil
}

// Put stores a value under the given key.
func (b *natsBucket) Put(key string, value []byte) error {
	_, err := b.kv.Put(key, value)
	return err
}

// Create puts a key only if it doesn't exist (Create operation).
func (b *natsBucket) Create(key string, value []byte) (uint64, error) {
	return b.kv.Create(key, value)
}

// Update updates a key with CAS (Compare-And-Swap).
func (b *natsBucket) Update(key string, value []byte, revision uint64) (uint64, error) {
	return b.kv.Update(key, value, revision)
}

// Delete removes a key.
func (b *natsBucket) Delete(key string) error {
	return b.kv.Delete(key)
}

// Keys returns all keys in the bucket.
func (b *natsBucket) Keys() ([]string, error) {
	return b.kv.Keys()
}

// Watch starts watching for changes in the bucket.
func (b *natsBucket) Watch(keyPrefix string) (BucketWatcher, error) {
	watcher, err := b.kv.Watch(keyPrefix)
	if err != nil {
		return nil, err
	}

	return &natsBucketWatcher{watcher: watcher}, nil
}

// WatchAll watches all keys in the bucket.
func (b *natsBucket) WatchAll() (BucketWatcher, error) {
	watcher, err := b.kv.WatchAll()
	if err != nil {
		return nil, err
	}

	return &natsBucketWatcher{watcher: watcher}, nil
}

// Close closes the bucket.
func (b *natsBucket) Close() error {
	// NATS KV buckets are managed by JetStream, not by individual holders
	// We don't actually delete the bucket, just release our reference
	return nil
}

// natsBucketWatcher implements BucketWatcher using NATS KV watcher.
type natsBucketWatcher struct {
	watcher nats.KeyWatcher
	updates chan *BucketEntry
	once    sync.Once
}

// Updates returns a channel of bucket entry updates.
func (w *natsBucketWatcher) Updates() <-chan *BucketEntry {
	if w.updates == nil {
		w.updates = make(chan *BucketEntry, 32)
		go w.forwardUpdates()
	}
	return w.updates
}

// forwardUpdates forwards NATS watcher updates to our channel.
func (w *natsBucketWatcher) forwardUpdates() {
	for entry := range w.watcher.Updates() {
		if entry == nil {
			continue
		}

		var op EntryOperation
		switch entry.Operation() {
		case nats.KeyValuePut:
			op = EntryOperationPut
		case nats.KeyValueDelete:
			op = EntryOperationDelete
		case nats.KeyValuePurge:
			op = EntryOperationPurge
		default:
			continue
		}

		bEntry := &BucketEntry{
			Key:       entry.Key(),
			Value:     entry.Value(),
			Revision:  entry.Revision(),
			Operation: op,
		}

		select {
		case w.updates <- bEntry:
		default:
			// Channel full, drop the update
			// In production, you might want to handle this differently
		}
	}
}

// Stop stops watching.
func (w *natsBucketWatcher) Stop() {
	w.once.Do(func() {
		w.watcher.Stop()
		if w.updates != nil {
			close(w.updates)
		}
	})
}
