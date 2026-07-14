package ddl

// Shared DDL utilities.

import (
	"vaultdb/internal/core/storage"
)

func sanitizeObjectName(name string) (string, error) {
	if err := storage.ValidateObjectName(name); err != nil {
		return "", err
	}
	return name, nil
}
