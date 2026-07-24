package crm

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"vaultdb"
)

type CRMDatabase struct {
	db   *vaultdb.VaultDB
	mu   sync.Mutex
	path string
}

func InitCRMDatabase(dataDir string) (*CRMDatabase, error) {
	dbPath := filepath.Join(dataDir, "vaultcrm_db")
	db, err := vaultdb.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open VaultDB for CRM: %w", err)
	}

	crm := &CRMDatabase{
		db:   db,
		path: dbPath,
	}

	if err := crm.bootstrapSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to bootstrap CRM schema: %w", err)
	}

	return crm, nil
}

func (c *CRMDatabase) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

func (c *CRMDatabase) bootstrapSchema() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. Create Database
	c.db.Query("", "CREATE DATABASE crmdb;")

	// 2. Customers Table
	_, err := c.db.Query("crmdb", `CREATE TABLE crm_customers (
		id INT PRIMARY KEY,
		name TEXT,
		email TEXT,
		company TEXT,
		status TEXT,
		score FLOAT,
		metadata JSONB,
		created_at TEXT
	);`)
	_ = err

	// 3. Deals Table (Used to test HOT Updates when updating status/amount)
	_, _ = c.db.Query("crmdb", `CREATE TABLE crm_deals (
		id INT PRIMARY KEY,
		customer_id INT,
		title TEXT,
		amount FLOAT,
		stage TEXT,
		probability FLOAT,
		updated_at TEXT
	);`)

	// 4. Notes Table (Used for Full-Text Search / GIN Index)
	_, _ = c.db.Query("crmdb", `CREATE TABLE crm_notes (
		id INT PRIMARY KEY,
		customer_id INT,
		author TEXT,
		content TEXT,
		created_at TEXT
	);`)

	// 5. System Audit Logs
	_, _ = c.db.Query("crmdb", `CREATE TABLE crm_logs (
		id INT PRIMARY KEY,
		action TEXT,
		detail TEXT,
		created_at TEXT
	);`)

	// 6. Indexes
	c.db.Query("crmdb", "CREATE INDEX idx_deals_customer ON crm_deals (customer_id);")
	c.db.Query("crmdb", "CREATE INDEX idx_notes_fts ON crm_notes USING GIN (content);")

	// 7. Insert Initial Seed Data if empty
	res, err := c.db.Query("crmdb", "SELECT COUNT(*) FROM crm_customers;")
	if err == nil && res != nil && len(res.Rows) > 0 {
		if res.Rows[0][0] == "0" {
			c.seedInitialData()
		}
	} else {
		c.seedInitialData()
	}

	return nil
}

func (c *CRMDatabase) seedInitialData() {
	now := time.Now().Format(time.RFC3339)

	// Seed Customers
	customers := []string{
		fmt.Sprintf("(1, 'Алексей Смирнов', 'alex@techcorp.ru', 'TechCorp', 'Active', 85.5, '{\"segment\":\"Enterprise\", \"tier\":\"Gold\"}', '%s')", now),
		fmt.Sprintf("(2, 'Елена Васильева', 'elena@innovate.io', 'Innovate LLC', 'Lead', 62.0, '{\"segment\":\"SMB\", \"tier\":\"Silver\"}', '%s')", now),
		fmt.Sprintf("(3, 'Дмитрий Иванов', 'dmitry@globaldata.com', 'GlobalData', 'VIP', 94.0, '{\"segment\":\"Enterprise\", \"tier\":\"Platinum\"}', '%s')", now),
		fmt.Sprintf("(4, 'Ольга Петрова', 'olga@cybernet.ru', 'CyberNet', 'Prospect', 45.0, '{\"segment\":\"Startup\", \"tier\":\"Bronze\"}', '%s')", now),
	}
	for _, cust := range customers {
		c.db.Query("crmdb", fmt.Sprintf("INSERT INTO crm_customers VALUES %s;", cust))
	}

	// Seed Deals
	deals := []string{
		fmt.Sprintf("(101, 1, 'Поставка серверов VaultDB Enterprise', 1250000.0, 'Negotiation', 0.8, '%s')", now),
		fmt.Sprintf("(102, 2, 'Лицензия на аналитический модуль', 350000.0, 'Qualified', 0.5, '%s')", now),
		fmt.Sprintf("(103, 3, 'Контракт поддержки 24/7', 2800000.0, 'Closed Won', 1.0, '%s')", now),
		fmt.Sprintf("(104, 4, 'Пилотный проект миграции на VaultDB', 150000.0, 'Discovery', 0.3, '%s')", now),
	}
	for _, deal := range deals {
		c.db.Query("crmdb", fmt.Sprintf("INSERT INTO crm_deals VALUES %s;", deal))
	}

	// Seed Notes
	notes := []string{
		fmt.Sprintf("(1, 1, 'Менеджер Анна', 'Клиент проявил высокий интерес к репликации Raft и Zero-Allocation движку.', '%s')", now),
		fmt.Sprintf("(2, 2, 'Менеджер Иван', 'Обсуждали возможности миграции с PostgreSQL. Главный плюс - HOT и скорость вставки.', '%s')", now),
		fmt.Sprintf("(3, 3, 'Директор по продажам', 'Контракт подгружен и согласован с юристами. Поддержка включена.', '%s')", now),
	}
	for _, note := range notes {
		c.db.Query("crmdb", fmt.Sprintf("INSERT INTO crm_notes VALUES %s;", note))
	}
}

func (c *CRMDatabase) GetRawDB() *vaultdb.VaultDB {
	return c.db
}
