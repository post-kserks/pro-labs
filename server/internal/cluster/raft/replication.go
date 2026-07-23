package raft

import (
	"context"
	"fmt"
	"sync"
	"vaultdb/internal/core/wal"
)

type Replicator struct {
	node *RaftNode
	wal  *wal.WAL
	SynchronousCommit bool
}

func NewReplicator(node *RaftNode, w *wal.WAL) *Replicator {
	return &Replicator{
		node: node,
		wal:  w,
		SynchronousCommit: true,
	}
}

// AppendWithTx wraps wal.AppendWithTx with synchronous quorum replication.
func (r *Replicator) AppendWithTx(ctx context.Context, txID uint64, opType byte, payload interface{}) (uint64, error) {
	r.node.mu.Lock()
	isLeader := r.node.State == Leader
	peers := make(map[string]Peer)
	for k, v := range r.node.Peers {
		peers[k] = v
	}
	term := r.node.CurrentTerm
	id := r.node.ID
	r.node.mu.Unlock()

	if !isLeader {
		return 0, fmt.Errorf("not a leader")
	}

	// 1. Write to local WAL
	lsn, err := r.wal.AppendWithTx(txID, opType, payload)
	if err != nil {
		return 0, fmt.Errorf("local wal append failed: %w", err)
	}

	// 2. Synchronous Replication (Quorum wait)
	totalNodes := len(peers) + 1
	majority := (totalNodes / 2) + 1
	acks := 1 // self

	if acks >= majority {
		return lsn, nil
	}


	if !r.SynchronousCommit {
		for _, peer := range peers {
			go func(p Peer) {
				p.AppendEntries(term, id)
			}(peer)
		}
		return lsn, nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, peer := range peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			// Mock log replication via AppendEntries
			success, _ := p.AppendEntries(term, id)
			if success {
				mu.Lock()
				acks++
				mu.Unlock()
			}
		}(peer)
	}

	// Wait for all responses or context timeout
	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()

	select {
	case <-c:
		if acks < majority {
			return 0, fmt.Errorf("failed to reach quorum, got %d/%d", acks, majority)
		}
		return lsn, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
