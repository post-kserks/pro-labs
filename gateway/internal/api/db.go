package api

import "fmt"

// queryMaps runs a SELECT-like statement and returns rows as column maps.
func (h *Handler) queryMaps(sql string) ([]map[string]string, error) {
	res, err := h.DB.Query(sql)
	if err != nil {
		return nil, err
	}
	return res.Maps(), nil
}

// nextID computes the next surrogate id for a table (max(id)+1).
// VaultDB has no auto-increment, and this is a single-writer demo, so a
// scan-and-increment is acceptable.
func (h *Handler) nextID(table string) (int, error) {
	res, err := h.DB.Query(fmt.Sprintf("SELECT id FROM %s;", table))
	if err != nil {
		return 0, err
	}
	max := 0
	for _, row := range res.Rows {
		if len(row) == 0 {
			continue
		}
		if v := atoi(row[0]); v > max {
			max = v
		}
	}
	return max + 1, nil
}

// dbError maps a VaultDB error to an HTTP error envelope.
func dbErrorCode(err error) (int, string) {
	return 500, "VAULTDB_ERROR"
}
