package storage

import "time"

type databaseMeta struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type versionedRowDisk struct {
	CreatedTx uint64        `json:"_vdb_created_tx"`
	DeletedTx uint64        `json:"_vdb_deleted_tx"`
	Data      []interface{} `json:"data"`
}

type tableDataDisk struct {
	Version int                `json:"version"`
	NextSeq int                `json:"next_seq"`
	Rows    []versionedRowDisk `json:"rows"`
}

type legacyTableDataDisk struct {
	Rows   [][]interface{} `json:"rows"`
	NextID int             `json:"next_id"`
}

type txLogEntryDisk struct {
	TxID      uint64 `json:"tx_id"`
	Timestamp string `json:"timestamp"`
	Op        string `json:"op"`
	Table     string `json:"table"`
}

type txLogDisk struct {
	Entries []txLogEntryDisk `json:"entries"`
}

type walCreateDatabasePayload struct {
	Name string `json:"name"`
}

type walDropDatabasePayload struct {
	Name string `json:"name"`
}

type walCreateTablePayload struct {
	DB     string      `json:"db"`
	Schema TableSchema `json:"schema"`
}

type walDropTablePayload struct {
	DB    string `json:"db"`
	Table string `json:"table"`
}

type walInsertPayload struct {
	DB    string          `json:"db"`
	Table string          `json:"table"`
	Rows  [][]interface{} `json:"rows"`
	Ts    string          `json:"ts"`
}

type walUpdatePayload struct {
	DB      string                 `json:"db"`
	Table   string                 `json:"table"`
	Indices []int                  `json:"indices"`
	Updates map[string]interface{} `json:"updates"`
	Ts      string                 `json:"ts"`
}

type walDeletePayload struct {
	DB      string `json:"db"`
	Table   string `json:"table"`
	Indices []int  `json:"indices"`
	Ts      string `json:"ts"`
}

type walVacuumPayload struct {
	DB    string `json:"db"`
	Table string `json:"table"`
}

type walAlterTablePayload struct {
	DB         string       `json:"db"`
	Table      string       `json:"table"`
	Op         string       `json:"op"`
	Column     ColumnSchema `json:"column,omitempty"`
	DefaultVal interface{}  `json:"default_val,omitempty"`
	OldName    string       `json:"old_name,omitempty"`
	NewName    string       `json:"new_name,omitempty"`
}

type indexMeta struct {
	Name     string `json:"name"`
	Column   string `json:"column"`
	ColIndex int    `json:"col_index"`
	Type     string `json:"type"`
}

type indexesMetadata struct {
	Indexes []indexMeta `json:"indexes"`
}

type refTableReader func(dbName, tableName string) ([]Row, error)
