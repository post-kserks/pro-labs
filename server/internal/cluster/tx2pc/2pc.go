package tx2pc

import (
	"context"
	"errors"
	"sync"
)

// TransactionState represents the 2PC state of a distributed transaction.
type TransactionState int

const (
	TxStateInit TransactionState = iota
	TxStatePrepared
	TxStateCommitted
	TxStateAborted
)

// TwoPhaseCoordinator manages distributed transactions across shards.
type TwoPhaseCoordinator struct {
	mu           sync.RWMutex
	transactions map[string]TransactionState
}

func NewTwoPhaseCoordinator() *TwoPhaseCoordinator {
	return &TwoPhaseCoordinator{
		transactions: make(map[string]TransactionState),
	}
}

// Prepare initiates the prepare phase for a transaction ID across all participants.
func (c *TwoPhaseCoordinator) Prepare(ctx context.Context, txID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if state, exists := c.transactions[txID]; exists && state != TxStateInit {
		return errors.New("transaction already prepared or finished")
	}

	// In a real system, this would send PREPARE messages to all participant nodes.
	// For this mock, we just transition to Prepared.
	c.transactions[txID] = TxStatePrepared
	return nil
}

// Commit forces all participants to commit.
func (c *TwoPhaseCoordinator) Commit(ctx context.Context, txID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.transactions[txID]
	if !exists {
		return errors.New("transaction not found")
	}
	if state != TxStatePrepared {
		return errors.New("transaction not prepared")
	}

	// In a real system, this would send COMMIT messages to all participant nodes.
	c.transactions[txID] = TxStateCommitted
	return nil
}

// Rollback forces all participants to abort.
func (c *TwoPhaseCoordinator) Rollback(ctx context.Context, txID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.transactions[txID]
	if !exists {
		return errors.New("transaction not found")
	}
	if state == TxStateCommitted {
		return errors.New("cannot rollback committed transaction")
	}

	// In a real system, this would send ROLLBACK messages to all participant nodes.
	c.transactions[txID] = TxStateAborted
	return nil
}
