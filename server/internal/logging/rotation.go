package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Rotator — ротатор лог-файлов.
type Rotator struct {
	mu          sync.Mutex
	file        *os.File
	filename    string
	maxSize     int64 // максимальный размер файла в байтах
	maxBackups  int   // максимальное количество бэкапов
	currentSize int64
}

// NewRotator создаёт новый ротатор.
func NewRotator(filename string, maxSizeMB int, maxBackups int) (*Rotator, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = 100 // по умолчанию 100 МБ
	}
	if maxBackups <= 0 {
		maxBackups = 5 // по умолчанию 5 бэкапов
	}

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	currentSize := info.Size()

	return &Rotator{
		file:        file,
		filename:    filename,
		maxSize:     int64(maxSizeMB) * 1024 * 1024,
		maxBackups:  maxBackups,
		currentSize: currentSize,
	}, nil
}

// Write записывает данные в лог-файл.
func (r *Rotator) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Проверяем нужна ли ротация
	if r.currentSize+int64(len(p)) > r.maxSize {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}

	n, err = r.file.Write(p)
	r.currentSize += int64(n)
	return n, err
}

// rotate выполняет ротацию лог-файла.
func (r *Rotator) rotate() error {
	// Закрываем текущий файл
	if err := r.file.Close(); err != nil {
		return err
	}

	// Удаляем самый старый бэкап
	r.removeOldestBackup()

	// Переименовываем текущий файл
	backupName := r.filename + fmt.Sprintf(".%s", time.Now().Format("2006-01-02T15-04-05"))
	if err := os.Rename(r.filename, backupName); err != nil {
		return err
	}

	// Создаём новый файл
	file, err := os.OpenFile(r.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return err
	}

	r.file = file
	r.currentSize = 0

	return nil
}

// removeOldestBackup удаляет самый старый бэкап.
func (r *Rotator) removeOldestBackup() {
	dir := filepath.Dir(r.filename)
	base := filepath.Base(r.filename)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > len(base) && entry.Name()[:len(base)] == base {
			backups = append(backups, entry.Name())
		}
	}

	// Удаляем старые бэкапы если их больше maxBackups
	for len(backups) > r.maxBackups {
		// Находим самый старый (по имени файла — там дата)
		oldest := backups[0]
		for _, b := range backups[1:] {
			if b < oldest {
				oldest = b
			}
		}
		os.Remove(filepath.Join(dir, oldest))

		// Удаляем из списка
		for i, b := range backups {
			if b == oldest {
				backups = append(backups[:i], backups[i+1:]...)
				break
			}
		}
	}
}

// Close закрывает ротатор.
func (r *Rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

// Sync синхронизирует файл на диск.
func (r *Rotator) Sync() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file != nil {
		return r.file.Sync()
	}
	return nil
}

// Writer возвращает io.Writer для использования с log/slog.
func (r *Rotator) Writer() io.Writer {
	return r
}

type AuditLogger struct {
	mu      sync.Mutex
	rotator *Rotator
}

func NewAuditLogger(rotator *Rotator) *AuditLogger {
	return &AuditLogger{rotator: rotator}
}

type ddlLogEntry struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Operation string `json:"operation"`
	Database  string `json:"database"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
}

func (a *AuditLogger) LogDDL(operation, database, target, detail string) {
	if a == nil || a.rotator == nil {
		return
	}
	entry := ddlLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      "ddl",
		Operation: operation,
		Database:  database,
		Target:    target,
		Detail:    detail,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rotator.Write(data)
}
