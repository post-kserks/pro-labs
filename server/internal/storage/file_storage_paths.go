package storage

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func (s *FileStorageEngine) databasesDir() string {
	return filepath.Join(s.rootDir, "databases")
}

func (s *FileStorageEngine) dbDir(dbName string) string {
	return filepath.Join(s.databasesDir(), dbName)
}

func (s *FileStorageEngine) tableDir(dbName, tableName string) string {
	return filepath.Join(s.dbDir(dbName), tableName)
}

func (s *FileStorageEngine) schemaPath(dbName, tableName string) string {
	return filepath.Join(s.tableDir(dbName, tableName), "_schema.json")
}

func (s *FileStorageEngine) dataPath(dbName, tableName string) string {
	return filepath.Join(s.tableDir(dbName, tableName), "_data.json")
}

func (s *FileStorageEngine) txLogPath(dbName string) string {
	return filepath.Join(s.dbDir(dbName), "_tx_log.json")
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func tableLockKey(dbName, tableName string) string {
	return dbName + "/" + tableName
}

func splitLockKey(key string) (dbName, tableName string) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}

func (s *FileStorageEngine) getTableLock(dbName, tableName string) *sync.RWMutex {
	key := tableLockKey(dbName, tableName)
	s.tableLocksMu.Lock()
	defer s.tableLocksMu.Unlock()
	if lock, ok := s.tableLocks[key]; ok {
		return lock
	}
	lock := &sync.RWMutex{}
	s.tableLocks[key] = lock
	return lock
}
