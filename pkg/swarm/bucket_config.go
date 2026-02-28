// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"github.com/sipeed/picoclaw/pkg/kv"
)

// BucketType identifies the type of bucket.
type BucketType string

const (
	BucketTypeMembers BucketType = "members"
	BucketTypeStatus  BucketType = "status"
	BucketTypeLeader  BucketType = "leader"
)

// DefaultBucketSpecs returns the default bucket specifications for all bucket types.
func DefaultBucketSpecs() map[BucketType]kv.BucketSpec {
	return map[BucketType]kv.BucketSpec{
		BucketTypeMembers: {
			Name:         "swarm_members",
			Description:  "PicoClaw Swarm Node Membership",
			TTL:          DefaultMemberTTL,
			MaxValueSize: 1024,
			Storage:      kv.StorageFile,
		},
		BucketTypeStatus: {
			Name:         "swarm_status",
			Description:  "PicoClaw Swarm Node Detailed Status",
			TTL:          DefaultDetailedStatusTTL,
			MaxValueSize: 4096,
			Storage:      kv.StorageMemory,
		},
		BucketTypeLeader: {
			Name:         "swarm_leader",
			Description:  "PicoClaw Swarm Leader Election Lock",
			TTL:          DefaultLeaderLockTTL,
			MaxValueSize: 512,
			Storage:      kv.StorageMemory,
		},
	}
}

// MemberBucketSpec returns the specification for the members bucket.
func MemberBucketSpec() kv.BucketSpec {
	return DefaultBucketSpecs()[BucketTypeMembers]
}

// StatusBucketSpec returns the specification for the status bucket.
func StatusBucketSpec() kv.BucketSpec {
	return DefaultBucketSpecs()[BucketTypeStatus]
}

// LeaderBucketSpec returns the specification for the leader bucket.
func LeaderBucketSpec() kv.BucketSpec {
	return DefaultBucketSpecs()[BucketTypeLeader]
}

// BucketSet provides access to all buckets used by the swarm.
type BucketSet struct {
	Members kv.Bucket
	Status  kv.Bucket
	Leader  kv.Bucket
	factory kv.BucketFactory
}

// NewBucketSet creates a new bucket set using the given factory.
func NewBucketSet(factory kv.BucketFactory) (*BucketSet, error) {
	bs := &BucketSet{factory: factory}

	var err error
	specs := DefaultBucketSpecs()

	// Create or get members bucket
	bs.Members, err = factory.CreateIfNotExists(specs[BucketTypeMembers])
	if err != nil {
		return nil, err
	}

	// Create or get status bucket
	bs.Status, err = factory.CreateIfNotExists(specs[BucketTypeStatus])
	if err != nil {
		return nil, err
	}

	// Create or get leader bucket
	bs.Leader, err = factory.CreateIfNotExists(specs[BucketTypeLeader])
	if err != nil {
		return nil, err
	}

	return bs, nil
}

// Close closes all buckets in the set.
func (bs *BucketSet) Close() error {
	return bs.factory.Close()
}
