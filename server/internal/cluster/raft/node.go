package raft

import (
	"sync"
	"time"
)

type State int

const (
	Follower State = iota
	Candidate
	Leader
)

type Peer interface {
	RequestVote(term uint64, candidateID string) (voteGranted bool, currentTerm uint64)
	AppendEntries(term uint64, leaderID string) (success bool, currentTerm uint64)
}

type RaftNode struct {
	mu sync.Mutex

	ID    string
	Peers map[string]Peer

	State       State
	CurrentTerm uint64
	VotedFor    string

	HeartbeatTimeout time.Duration
	lastHeartbeat    time.Time
}

func NewRaftNode(id string, timeout time.Duration) *RaftNode {
	return &RaftNode{
		ID:               id,
		Peers:            make(map[string]Peer),
		State:            Follower,
		HeartbeatTimeout: timeout,
		lastHeartbeat:    time.Now(),
	}
}

func (n *RaftNode) AddPeer(id string, peer Peer) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Peers[id] = peer
}

func (n *RaftNode) RequestVote(term uint64, candidateID string) (bool, uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if term > n.CurrentTerm {
		n.CurrentTerm = term
		n.State = Follower
		n.VotedFor = ""
	}

	if term == n.CurrentTerm && (n.VotedFor == "" || n.VotedFor == candidateID) {
		n.VotedFor = candidateID
		n.lastHeartbeat = time.Now()
		return true, n.CurrentTerm
	}

	return false, n.CurrentTerm
}

func (n *RaftNode) AppendEntries(term uint64, leaderID string) (bool, uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if term < n.CurrentTerm {
		return false, n.CurrentTerm
	}

	n.CurrentTerm = term
	n.State = Follower
	n.lastHeartbeat = time.Now()
	return true, n.CurrentTerm
}

func (n *RaftNode) Tick() {
	n.mu.Lock()

	if n.State == Leader {
		n.mu.Unlock()
		return // Leader sends heartbeats, doesn't timeout here
	}

	if time.Since(n.lastHeartbeat) >= n.HeartbeatTimeout {
		n.State = Candidate
		n.CurrentTerm++
		n.VotedFor = n.ID
		n.lastHeartbeat = time.Now()

		term := n.CurrentTerm
		candidateID := n.ID
		peers := make(map[string]Peer)
		for k, v := range n.Peers {
			peers[k] = v
		}
		n.mu.Unlock()

		votes := 1 // Vote for self
		for _, peer := range peers {
			granted, _ := peer.RequestVote(term, candidateID)
			if granted {
				votes++
			}
		}

		n.mu.Lock()
		// If still Candidate in the same term and gained majority
		if n.State == Candidate && n.CurrentTerm == term && votes > (len(peers)+1)/2 {
			n.State = Leader
		}
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()
}

func (n *RaftNode) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.State == Leader
}
