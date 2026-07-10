package storage

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

func FuzzCatalogJSON(f *testing.F) {
	// Seed with valid catalog JSON
	f.Add([]byte(`{"current_tx_id":1,"last_modified":{},"row_counts":{},"tx_times":[],"checkpoint_lsn":0}`))
	f.Add([]byte(`{"current_tx_id":0,"last_modified":{"t":1},"row_counts":{"t":5},"tx_times":[{"tx":1,"ts":1000}],"checkpoint_lsn":42}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`""`))
	f.Add([]byte(`invalid json`))
	f.Add([]byte(`{`))
	f.Add([]byte(`}`))
	f.Add([]byte(`{"current_tx_id": "not_a_number"}`))
	f.Add([]byte(`{"current_tx_id": -1}`))
	f.Add([]byte(`{"current_tx_id": 9999999999999999999}`))

	// TableSchema seeds
	f.Add([]byte(`{"name":"t","database":"db","columns":[{"name":"id","type":"INT","nullable":false}],"created_at":"2024-01-01T00:00:00Z"}`))
	f.Add([]byte(`{"name":"","database":"","columns":[],"created_at":"0001-01-01T00:00:00Z"}`))
	f.Add([]byte(`{"name":"t","database":"db","columns":[{"name":"id","type":"INT","nullable":false,"default":"42","auto_increment":true}],"constraints":[{"type":"PRIMARY KEY","columns":["id"]}],"rls_enabled":true,"policies":[{"name":"p","command":"SELECT","using":"true"}],"partition_by":{"type":"RANGE","column":"id","partitions":null}}`))
	f.Add([]byte(`{"name":"t","database":"db","columns":[{"name":"data","type":"JSONB","nullable":true},{"name":"tags","type":"ARRAY","nullable":true},{"name":"score","type":"DECIMAL","nullable":false},{"name":"blob","type":"BLOB","nullable":true}],"constraints":[],"created_at":"2024-06-15T12:00:00Z"}`))

	// Edge cases
	f.Add([]byte{})
	f.Add([]byte(strings.Repeat("a", 10000)))
	f.Add([]byte(`{"columns":[{"name":` + strings.Repeat(`"a",`, 500) + `"z"}]}`))

	// Binary data
	f.Add([]byte{0x00, 0x01, 0x02, 0xff, 0xfe})

	// Deeply nested
	f.Add([]byte(`{"columns":[{"constraints":[{"columns":[` + strings.Repeat(`"x",`, 100) + `"y"]}]}]}`))

	// Random template-based seeds
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		txID := rng.Uint64()
		rows := rng.Intn(1000)
		f.Add([]byte(`{"current_tx_id":` + strings.Repeat("1", int(1+txID%20)) + `,"row_counts":{"t":` + strings.Repeat("1", 1+rows%10) + `}}`))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("JSON parse panicked on %q: %v", string(data), r)
			}
		}()

		// Try to unmarshal as pageCatalog
		var catalog pageCatalog
		_ = json.Unmarshal(data, &catalog)

		// Try to unmarshal as TableSchema
		var schema TableSchema
		_ = json.Unmarshal(data, &schema)

		// Try to unmarshal as []TableSchema
		var schemas []TableSchema
		_ = json.Unmarshal(data, &schemas)

		// Try to unmarshal as map[string]interface{} (generic)
		var m map[string]interface{}
		_ = json.Unmarshal(data, &m)
	})
}
