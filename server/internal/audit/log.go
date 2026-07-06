package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Entry represents one audit log record.
type Entry struct {
	ID         uint64    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	Target     string    `json:"target"`
	Detail     string    `json:"detail,omitempty"`
	PrevHash   string    `json:"prev_hash"`
	EntryHash  string    `json:"entry_hash"`
}

// HashChain computes the hash for an audit entry.
func (e *Entry) HashChain(prevHash string) string {
	payload := fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s",
		e.ID, e.OccurredAt.Format(time.RFC3339Nano),
		e.Actor, e.Action, e.Target, e.Detail, prevHash)
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])
}

// Log is the interface for audit logging.
// Implementations can be file-based or table-based.
type Log interface {
	Append(entry Entry) error
	ReadAll() ([]Entry, error)
	LastHash() (string, error)
}
