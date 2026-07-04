package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// Binary catalog format
// ─────────────────────────────────────────────────────────────────────────────
// Header: magic "VDBC" (4B) + version (4B LE) + table_count (4B LE)
// For each table: name_len (2B LE) + name + column_count (2B LE) + row_count (8B LE) + flags (4B LE)
// For each column: name_len (2B LE) + name + type_len (2B LE) + type + flags (4B LE)
//
// Flags:
//   bit 0: NOT_NULL
//   bit 1: PRIMARY_KEY
//   bit 2: AUTO_INCREMENT
//   bit 3: IS_COMPUTED
//   bit 4: RLSEnabled (table-level)
//   bit 5: HAS_CONSTRAINTS

const (
	binaryCatalogMagic   = "VDBC"
	binaryCatalogVersion = 1

	catalogFlagNotNull      uint32 = 1 << 0
	catalogFlagPrimaryKey   uint32 = 1 << 1
	catalogFlagAutoInc      uint32 = 1 << 2
	catalogFlagComputed     uint32 = 1 << 3
	catalogFlagRLSEnabled   uint32 = 1 << 4
	catalogFlagHasConst     uint32 = 1 << 5
)

type BinaryCatalog struct {
	Magic       [4]byte
	Version     uint32
	TableCount  uint32
	Tables      []BinaryTableEntry
}

type BinaryTableEntry struct {
	NameLen     uint16
	Name        []byte
	ColumnCount uint16
	Columns     []BinaryColumnEntry
	RowCount    uint64
	Flags       uint32
}

type BinaryColumnEntry struct {
	NameLen uint16
	Name    []byte
	TypeLen uint16
	Type    []byte
	Flags   uint32
}

// columnFlags returns the flags for a ColumnSchema.
func columnFlags(col ColumnSchema) uint32 {
	var f uint32
	if col.NotNull {
		f |= catalogFlagNotNull
	}
	if col.PrimaryKey {
		f |= catalogFlagPrimaryKey
	}
	if col.AutoIncrement {
		f |= catalogFlagAutoInc
	}
	if col.IsComputed {
		f |= catalogFlagComputed
	}
	return f
}

// tableFlags returns the flags for a TableSchema.
func tableFlags(ts TableSchema) uint32 {
	var f uint32
	if ts.RLSEnabled {
		f |= catalogFlagRLSEnabled
	}
	if len(ts.Constraints) > 0 {
		f |= catalogFlagHasConst
	}
	return f
}

// Marshal serializes a BinaryCatalog to bytes.
func (c *BinaryCatalog) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(12) // header minimum

	buf.Write(c.Magic[:])
	_ = binary.Write(&buf, binary.LittleEndian, c.Version)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(c.Tables)))

	for i := range c.Tables {
		t := &c.Tables[i]
		t.ColumnCount = uint16(len(t.Columns))
		_ = binary.Write(&buf, binary.LittleEndian, t.NameLen)
		buf.Write(t.Name)
		_ = binary.Write(&buf, binary.LittleEndian, t.ColumnCount)
		_ = binary.Write(&buf, binary.LittleEndian, t.RowCount)
		_ = binary.Write(&buf, binary.LittleEndian, t.Flags)

		for j := range t.Columns {
			col := &t.Columns[j]
			_ = binary.Write(&buf, binary.LittleEndian, col.NameLen)
			buf.Write(col.Name)
			_ = binary.Write(&buf, binary.LittleEndian, col.TypeLen)
			buf.Write(col.Type)
			_ = binary.Write(&buf, binary.LittleEndian, col.Flags)
		}
	}

	return buf.Bytes(), nil
}

// UnmarshalBinaryCatalog deserializes a BinaryCatalog from bytes.
func UnmarshalBinaryCatalog(data []byte) (*BinaryCatalog, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("catalog: too small (%d bytes)", len(data))
	}

	cat := &BinaryCatalog{}
	copy(cat.Magic[:], data[0:4])
	if string(cat.Magic[:]) != binaryCatalogMagic {
		return nil, fmt.Errorf("catalog: invalid magic %q", string(cat.Magic[:]))
	}

	cat.Version = binary.LittleEndian.Uint32(data[4:8])
	tableCount := binary.LittleEndian.Uint32(data[8:12])
	cat.TableCount = tableCount

	offset := 12
	for i := uint32(0); i < tableCount; i++ {
		entry, bytesRead, err := unmarshalTableEntry(data[offset:])
		if err != nil {
			return nil, fmt.Errorf("catalog: table %d: %w", i, err)
		}
		cat.Tables = append(cat.Tables, *entry)
		offset += bytesRead
	}

	return cat, nil
}

func unmarshalTableEntry(data []byte) (*BinaryTableEntry, int, error) {
	if len(data) < 2 {
		return nil, 0, fmt.Errorf("table name length truncated")
	}
	nameLen := int(binary.LittleEndian.Uint16(data[0:2]))
	offset := 2

	if len(data) < offset+nameLen {
		return nil, 0, fmt.Errorf("table name truncated")
	}
	name := make([]byte, nameLen)
	copy(name, data[offset:offset+nameLen])
	offset += nameLen

	if len(data) < offset+14 { // colCount(2) + rowCount(8) + flags(4)
		return nil, 0, fmt.Errorf("table header truncated")
	}
	colCount := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2
	rowCount := binary.LittleEndian.Uint64(data[offset : offset+8])
	offset += 8
	flags := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	entry := &BinaryTableEntry{
		NameLen:     uint16(nameLen),
		Name:        name,
		ColumnCount: colCount,
		RowCount:    rowCount,
		Flags:       flags,
	}

	for i := uint16(0); i < colCount; i++ {
		col, n, err := unmarshalColumnEntry(data[offset:])
		if err != nil {
			return nil, 0, fmt.Errorf("column %d: %w", i, err)
		}
		entry.Columns = append(entry.Columns, *col)
		offset += n
	}

	return entry, offset, nil
}

func unmarshalColumnEntry(data []byte) (*BinaryColumnEntry, int, error) {
	if len(data) < 2 {
		return nil, 0, fmt.Errorf("column name length truncated")
	}
	nameLen := int(binary.LittleEndian.Uint16(data[0:2]))
	offset := 2

	if len(data) < offset+nameLen {
		return nil, 0, fmt.Errorf("column name truncated")
	}
	name := make([]byte, nameLen)
	copy(name, data[offset:offset+nameLen])
	offset += nameLen

	if len(data) < offset+2 {
		return nil, 0, fmt.Errorf("column type length truncated")
	}
	typeLen := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	offset += 2

	if len(data) < offset+typeLen {
		return nil, 0, fmt.Errorf("column type truncated")
	}
	typ := make([]byte, typeLen)
	copy(typ, data[offset:offset+typeLen])
	offset += typeLen

	if len(data) < offset+4 {
		return nil, 0, fmt.Errorf("column flags truncated")
	}
	flags := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	return &BinaryColumnEntry{
		NameLen: uint16(nameLen),
		Name:    name,
		TypeLen: uint16(typeLen),
		Type:    typ,
		Flags:   flags,
	}, offset, nil
}

// CachedCatalog wraps a BinaryCatalog with a read-heavy cache and dirty tracking.
type CachedCatalog struct {
	data     []byte
	parsed   *BinaryCatalog
	modified bool
	mu       sync.RWMutex
}

// NewCachedCatalog creates a CachedCatalog from raw binary data.
func NewCachedCatalog(data []byte) (*CachedCatalog, error) {
	parsed, err := UnmarshalBinaryCatalog(data)
	if err != nil {
		return nil, err
	}
	return &CachedCatalog{data: data, parsed: parsed}, nil
}

// GetTable returns a table entry by name.
func (cc *CachedCatalog) GetTable(name string) (*BinaryTableEntry, bool) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	for i := range cc.parsed.Tables {
		if string(cc.parsed.Tables[i].Name) == name {
			return &cc.parsed.Tables[i], true
		}
	}
	return nil, false
}

// SetTable adds or replaces a table entry.
func (cc *CachedCatalog) SetTable(entry BinaryTableEntry) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	for i, t := range cc.parsed.Tables {
		if string(t.Name) == string(entry.Name) {
			cc.parsed.Tables[i] = entry
			cc.modified = true
			return
		}
	}
	cc.parsed.Tables = append(cc.parsed.Tables, entry)
	cc.parsed.TableCount = uint32(len(cc.parsed.Tables))
	cc.modified = true
}

// RemoveTable removes a table entry by name.
func (cc *CachedCatalog) RemoveTable(name string) bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	for i, t := range cc.parsed.Tables {
		if string(t.Name) == name {
			cc.parsed.Tables = append(cc.parsed.Tables[:i], cc.parsed.Tables[i+1:]...)
			cc.parsed.TableCount = uint32(len(cc.parsed.Tables))
			cc.modified = true
			return true
		}
	}
	return false
}

// Marshal returns the binary representation, re-serializing if modified.
func (cc *CachedCatalog) Marshal() ([]byte, error) {
	cc.mu.RLock()
	if !cc.modified {
		data := cc.data
		cc.mu.RUnlock()
		return data, nil
	}
	cc.mu.RUnlock()

	cc.mu.Lock()
	defer cc.mu.Unlock()
	data, err := cc.parsed.Marshal()
	if err != nil {
		return nil, err
	}
	cc.data = data
	cc.modified = false
	return data, nil
}

// TableNames returns sorted table names.
func (cc *CachedCatalog) TableNames() []string {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	names := make([]string, len(cc.parsed.Tables))
	for i, t := range cc.parsed.Tables {
		names[i] = string(t.Name)
	}
	sort.Strings(names)
	return names
}

// Len returns the number of tables.
func (cc *CachedCatalog) Len() int {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return len(cc.parsed.Tables)
}

// MarshalCatalog builds a BinaryCatalog from the legacy pageCatalog + schemas map.
func MarshalCatalog(cat *pageCatalog, schemas map[string]*TableSchema) ([]byte, error) {
	bc := &BinaryCatalog{
		Magic:   [4]byte{'V', 'D', 'B', 'C'},
		Version: binaryCatalogVersion,
	}

	// Collect all keys from schemas and row counts
	keySet := make(map[string]bool)
	for key := range schemas {
		keySet[key] = true
	}
	for key := range cat.RowCounts {
		keySet[key] = true
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		entry := BinaryTableEntry{
			Name: []byte(key),
		}
		entry.NameLen = uint16(len(entry.Name))

		if rc, ok := cat.RowCounts[key]; ok {
			entry.RowCount = uint64(rc)
		}

		if schema, ok := schemas[key]; ok {
			entry.Flags = tableFlags(*schema)
			entry.ColumnCount = uint16(len(schema.Columns))
			for _, col := range schema.Columns {
				ce := BinaryColumnEntry{
					Name: []byte(col.Name),
					Type: []byte(col.Type),
					Flags: columnFlags(col),
				}
				ce.NameLen = uint16(len(ce.Name))
				ce.TypeLen = uint16(len(ce.Type))
				entry.Columns = append(entry.Columns, ce)
			}
		}

		bc.Tables = append(bc.Tables, entry)
	}
	bc.TableCount = uint32(len(bc.Tables))

	return bc.Marshal()
}

// UnmarshalToPageCatalog converts a BinaryCatalog back to the legacy pageCatalog + schemas.
func UnmarshalToPageCatalog(bc *BinaryCatalog) (*pageCatalog, map[string]*TableSchema) {
	cat := &pageCatalog{
		LastModified: make(map[string]uint64),
		RowCounts:    make(map[string]int),
	}
	schemas := make(map[string]*TableSchema)

	for _, t := range bc.Tables {
		key := string(t.Name)
		cat.RowCounts[key] = int(t.RowCount)

		if t.ColumnCount > 0 {
			schema := &TableSchema{
				Name:      key,
				RLSEnabled: t.Flags&catalogFlagRLSEnabled != 0,
			}
			for _, col := range t.Columns {
				schema.Columns = append(schema.Columns, ColumnSchema{
					Name:          string(col.Name),
					Type:          string(col.Type),
					NotNull:       col.Flags&catalogFlagNotNull != 0,
					PrimaryKey:    col.Flags&catalogFlagPrimaryKey != 0,
					AutoIncrement: col.Flags&catalogFlagAutoInc != 0,
					IsComputed:    col.Flags&catalogFlagComputed != 0,
				})
			}
			schemas[key] = schema
		}
	}

	return cat, schemas
}
