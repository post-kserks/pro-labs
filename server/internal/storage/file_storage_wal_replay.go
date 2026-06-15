package storage

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"vaultdb/internal/wal"
)

func (s *FileStorageEngine) recoverFromWAL() error {
	if s.wal == nil {
		return nil
	}

	entries, err := s.wal.Recover()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		slog.Info("WAL: nothing to recover")
		return nil
	}

	slog.Info("WAL recovery started", "entries", len(entries))
	replayed := 0
	for _, entry := range entries {
		if err := s.replayWALEntry(entry); err != nil {
			slog.Warn("WAL replay error", "tx_id", entry.TxID, "op_type", entry.OpType, "error", err)
			continue
		}
		replayed++
	}

	if err := s.flushDataDirty(); err != nil {
		slog.Warn("flush data after recovery failed", "error", err)
		return nil
	}
	if err := s.flushTxLogDirty(); err != nil {
		slog.Warn("flush tx log after recovery failed", "error", err)
		return nil
	}
	_ = s.wal.Checkpoint()
	slog.Info("WAL recovery complete", "total_entries", len(entries), "replayed", replayed)
	return nil
}

func (s *FileStorageEngine) replayWALEntry(entry wal.Entry) error {
	switch entry.OpType {
	case wal.OpCreateDatabase:
		var p walCreateDatabasePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.createDatabaseInternal(p.Name)

	case wal.OpDropDatabase:
		var p walDropDatabasePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.dropDatabaseInternal(p.Name)

	case wal.OpCreateTable:
		var p walCreateTablePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.createTableInternal(p.DB, p.Schema)

	case wal.OpDropTable:
		var p walDropTablePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.dropTableInternal(p.DB, p.Table)

	case wal.OpInsert:
		var p walInsertPayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayInsert(entry.TxID, p)

	case wal.OpUpdate:
		var p walUpdatePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayUpdate(entry.TxID, p)

	case wal.OpDelete:
		var p walDeletePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayDelete(entry.TxID, p)

	case wal.OpVacuum:
		var p walVacuumPayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayVacuum(p)

	case wal.OpAlterTable:
		var p walAlterTablePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayAlterTable(p)

	default:
		return fmt.Errorf("unknown WAL op: 0x%02X", entry.OpType)
	}
}

func (s *FileStorageEngine) replayAlterTable(p walAlterTablePayload) error {
	switch p.Op {
	case "ADD_COLUMN":
		lock := s.getTableLock(p.DB, p.Table)
		lock.Lock()
		defer lock.Unlock()
		return s.applyAlterTableAddColumnLocked(p.DB, p.Table, p.Column, p.DefaultVal)
	case "DROP_COLUMN":
		lock := s.getTableLock(p.DB, p.Table)
		lock.Lock()
		defer lock.Unlock()
		return s.applyAlterTableDropColumnLocked(p.DB, p.Table, p.Column.Name)
	case "RENAME_COLUMN":
		lock := s.getTableLock(p.DB, p.Table)
		lock.Lock()
		defer lock.Unlock()
		return s.applyAlterTableRenameColumnLocked(p.DB, p.Table, p.OldName, p.NewName)
	case "RENAME_TABLE":
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.applyAlterTableRenameTableLocked(p.DB, p.OldName, p.NewName)
	default:
		return fmt.Errorf("unknown ALTER TABLE op: %s", p.Op)
	}
}

func (s *FileStorageEngine) replayVacuum(p walVacuumPayload) error {
	_, err := s.Vacuum(p.DB, p.Table)
	return err
}

func (s *FileStorageEngine) replayInsert(txID uint64, p walInsertPayload) error {
	lock := s.getTableLock(p.DB, p.Table)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(p.DB, p.Table)
	if err != nil {
		return err
	}
	data, err := s.readVersionedData(p.DB, p.Table, schema)
	if err != nil {
		return err
	}

	for _, row := range p.Rows {
		if len(row) != len(schema.Columns) {
			return fmt.Errorf("replay insert width mismatch for table '%s'", p.Table)
		}
	}

	affected, err := s.applyInsertLocked(p.DB, p.Table, data, p.Rows, txID, true)
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	if err := s.writeVersionedData(p.DB, p.Table, data); err != nil {
		return err
	}
	ts := parsePayloadTimestamp(p.Ts)
	return s.appendTxLog(p.DB, TxLogEntry{TxID: txID, Timestamp: ts, Op: "INSERT", Table: p.Table})
}

func (s *FileStorageEngine) replayUpdate(txID uint64, p walUpdatePayload) error {
	lock := s.getTableLock(p.DB, p.Table)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(p.DB, p.Table)
	if err != nil {
		return err
	}
	data, err := s.readVersionedData(p.DB, p.Table, schema)
	if err != nil {
		return err
	}

	normalizedUpdates, _, err := buildNormalizedUpdatesFromInterfaces(schema, p.Updates)
	if err != nil {
		return err
	}
	affected, err := s.applyUpdateLocked(p.DB, p.Table, data, schema, p.Indices, normalizedUpdates, txID, true)
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	if err := s.writeVersionedData(p.DB, p.Table, data); err != nil {
		return err
	}
	ts := parsePayloadTimestamp(p.Ts)
	return s.appendTxLog(p.DB, TxLogEntry{TxID: txID, Timestamp: ts, Op: "UPDATE", Table: p.Table})
}

func (s *FileStorageEngine) replayDelete(txID uint64, p walDeletePayload) error {
	lock := s.getTableLock(p.DB, p.Table)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(p.DB, p.Table)
	if err != nil {
		return err
	}
	data, err := s.readVersionedData(p.DB, p.Table, schema)
	if err != nil {
		return err
	}

	affected, err := s.applyDeleteLocked(p.DB, p.Table, data, p.Indices, txID, true)
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	if err := s.writeVersionedData(p.DB, p.Table, data); err != nil {
		return err
	}
	ts := parsePayloadTimestamp(p.Ts)
	return s.appendTxLog(p.DB, TxLogEntry{TxID: txID, Timestamp: ts, Op: "DELETE", Table: p.Table})
}
