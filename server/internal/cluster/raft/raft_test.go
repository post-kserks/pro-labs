package raft

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vaultdb/internal/core/wal"
)

func TestLeaderElection(t *testing.T) {
	n1 := NewRaftNode("node1", 50*time.Millisecond)
	n2 := NewRaftNode("node2", 50*time.Millisecond)
	n3 := NewRaftNode("node3", 50*time.Millisecond)

	n1.AddPeer("node2", n2)
	n1.AddPeer("node3", n3)

	n2.AddPeer("node1", n1)
	n2.AddPeer("node3", n3)

	n3.AddPeer("node1", n1)
	n3.AddPeer("node2", n2)

	// Trigger election on n1
	time.Sleep(100 * time.Millisecond)
	n1.Tick()

	if !n1.IsLeader() {
		t.Fatalf("Expected node1 to be leader")
	}

	if n2.IsLeader() || n3.IsLeader() {
		t.Fatalf("Expected node2 and node3 to be followers")
	}
}

func TestReplication(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "raft_wal_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	w, err := wal.Open(filepath.Join(tmpDir, "test.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	n1 := NewRaftNode("node1", 50*time.Millisecond)
	n2 := NewRaftNode("node2", 50*time.Millisecond)
	n3 := NewRaftNode("node3", 50*time.Millisecond)

	n1.AddPeer("node2", n2)
	n1.AddPeer("node3", n3)

	n2.AddPeer("node1", n1)
	n2.AddPeer("node3", n3)

	n3.AddPeer("node1", n1)
	n3.AddPeer("node2", n2)

	// Make n1 leader manually for test
	n1.mu.Lock()
	n1.State = Leader
	n1.CurrentTerm = 1
	n1.mu.Unlock()

	r := NewReplicator(n1, w)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	lsn, err := r.AppendWithTx(ctx, 1, wal.OpInsert, map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatalf("AppendWithTx failed: %v", err)
	}

	if lsn == 0 {
		t.Fatalf("Expected non-zero lsn")
	}
}
