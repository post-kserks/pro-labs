package wal

import (
	"io"
)

// LogicalEventType represents the type of logical change.
type LogicalEventType string

const (
	EventInsert LogicalEventType = "INSERT"
	EventDelete LogicalEventType = "DELETE"
	EventCommit LogicalEventType = "COMMIT"
)

// LogicalEvent represents a single row-level or transaction-level event for CDC.
type LogicalEvent struct {
	TxID      uint64
	Type      LogicalEventType
	DB        string
	Table     string
	TupleData []byte // only for INSERT
	Timestamp int64  // only for COMMIT
}

// LogicalDecoder reads a WAL file and groups row-level operations by transaction.
// When a transaction commits, it emits the logical events to the subscriber.
type LogicalDecoder struct {
	// activeTx tracks operations for transactions that haven't committed yet
	activeTx map[uint64][]LogicalEvent
}

// NewLogicalDecoder creates a new logical decoder.
func NewLogicalDecoder() *LogicalDecoder {
	return &LogicalDecoder{
		activeTx: make(map[uint64][]LogicalEvent),
	}
}

// DecodeFile reads a WAL file sequentially and calls callback fn for each committed event.
func (d *LogicalDecoder) DecodeFile(walPath string, fn func(event LogicalEvent)) error {
	w, err := Open(walPath)
	if err != nil {
		return err
	}
	defer w.Close()

	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	for {
		entry, _, err := w.readEntryFrom(w.file)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return err
		}

		payload, err := DecodeWALPayload(entry.Payload, entry.OpType)
		if err != nil {
			continue // skip unknown or corrupt payload
		}

		switch entry.OpType {
		case OpPageInsert:
			p, ok := payload.(WALPageInsertPayload)
			if ok {
				d.activeTx[entry.TxID] = append(d.activeTx[entry.TxID], LogicalEvent{
					TxID:      entry.TxID,
					Type:      EventInsert,
					DB:        p.DB,
					Table:     p.Table,
					TupleData: p.TupleData,
				})
			}
		case OpPageDelete, OpPageUpdateXMax:
			p, ok := payload.(WALPageDeletePayload)
			if ok {
				d.activeTx[entry.TxID] = append(d.activeTx[entry.TxID], LogicalEvent{
					TxID:  entry.TxID,
					Type:  EventDelete,
					DB:    p.DB,
					Table: p.Table,
				})
			}
		case OpCommit:
			p, ok := payload.(CommitPayload)
			if ok {
				events := d.activeTx[entry.TxID]
				// Emit row events
				for _, ev := range events {
					fn(ev)
				}
				// Emit commit event
				fn(LogicalEvent{
					TxID:      entry.TxID,
					Type:      EventCommit,
					Timestamp: p.Timestamp,
				})
				delete(d.activeTx, entry.TxID)
			}
		case OpAbort:
			delete(d.activeTx, entry.TxID)
		}
	}
	return nil
}
