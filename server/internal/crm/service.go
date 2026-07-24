package crm

import (
	"fmt"
	"sync/atomic"
	"time"

	"vaultdb"
)

type CRMService struct {
	crmDB  *CRMDatabase
	nextID atomic.Int64
}

func NewCRMService(crmDB *CRMDatabase) *CRMService {
	s := &CRMService{
		crmDB: crmDB,
	}
	s.nextID.Store(1000)
	return s
}

type Customer struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Email     string  `json:"email"`
	Company   string  `json:"company"`
	Status    string  `json:"status"`
	Score     float64 `json:"score"`
	Metadata  string  `json:"metadata"`
	CreatedAt string  `json:"created_at"`
}

type Deal struct {
	ID          int64   `json:"id"`
	CustomerID  int64   `json:"customer_id"`
	Title       string  `json:"title"`
	Amount      float64 `json:"amount"`
	Stage       string  `json:"stage"`
	Probability float64 `json:"probability"`
	UpdatedAt   string  `json:"updated_at"`
}

type Note struct {
	ID         int64  `json:"id"`
	CustomerID int64  `json:"customer_id"`
	Author     string `json:"author"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
}

type SystemStats struct {
	BufferPoolPages int    `json:"buffer_pool_pages"`
	ActiveTx        int    `json:"active_tx"`
	CatalogTables   int    `json:"catalog_tables"`
	TotalCustomers  int64  `json:"total_customers"`
	TotalDeals      int64  `json:"total_deals"`
	RaftStatus      string `json:"raft_status"`
	EngineMode      string `json:"engine_mode"`
}

func (s *CRMService) GetCustomers() ([]Customer, error) {
	db := s.crmDB.GetRawDB()
	res, err := db.Query("crmdb", "SELECT id, name, email, company, status, score, metadata, created_at FROM crm_customers ORDER BY id DESC;")
	if err != nil {
		return nil, err
	}

	var customers []Customer
	for _, row := range res.Rows {
		if len(row) < 8 {
			continue
		}
		cust := Customer{
			ID:        toInt64(row[0]),
			Name:      fmt.Sprintf("%v", row[1]),
			Email:     fmt.Sprintf("%v", row[2]),
			Company:   fmt.Sprintf("%v", row[3]),
			Status:    fmt.Sprintf("%v", row[4]),
			Score:     toFloat64(row[5]),
			Metadata:  fmt.Sprintf("%v", row[6]),
			CreatedAt: fmt.Sprintf("%v", row[7]),
		}
		customers = append(customers, cust)
	}
	return customers, nil
}

func (s *CRMService) AddCustomer(name, email, company, status string, metadata string) (*Customer, error) {
	id := s.nextID.Add(1)
	now := time.Now().Format(time.RFC3339)
	score := s.calculateLeadScoreWasm(name, company, metadata)

	query := fmt.Sprintf("INSERT INTO crm_customers VALUES (%d, '%s', '%s', '%s', '%s', %f, '%s', '%s');",
		id, name, email, company, status, score, metadata, now)

	db := s.crmDB.GetRawDB()
	_, err := db.Query("crmdb", query)
	if err != nil {
		return nil, err
	}

	cust := &Customer{
		ID:        id,
		Name:      name,
		Email:     email,
		Company:   company,
		Status:    status,
		Score:     score,
		Metadata:  metadata,
		CreatedAt: now,
	}
	return cust, nil
}

func (s *CRMService) GetDeals() ([]Deal, error) {
	db := s.crmDB.GetRawDB()
	res, err := db.Query("crmdb", "SELECT id, customer_id, title, amount, stage, probability, updated_at FROM crm_deals ORDER BY id DESC;")
	if err != nil {
		return nil, err
	}

	var deals []Deal
	for _, row := range res.Rows {
		if len(row) < 7 {
			continue
		}
		d := Deal{
			ID:          toInt64(row[0]),
			CustomerID:  toInt64(row[1]),
			Title:       fmt.Sprintf("%v", row[2]),
			Amount:      toFloat64(row[3]),
			Stage:       fmt.Sprintf("%v", row[4]),
			Probability: toFloat64(row[5]),
			UpdatedAt:   fmt.Sprintf("%v", row[6]),
		}
		deals = append(deals, d)
	}
	return deals, nil
}

// UpdateDealStage demonstrates HOT (Heap-Only Tuples) updates in VaultDB when stage/probability changes.
func (s *CRMService) UpdateDealStage(dealID int64, newStage string, newProbability float64) error {
	db := s.crmDB.GetRawDB()
	now := time.Now().Format(time.RFC3339)
	query := fmt.Sprintf("UPDATE crm_deals SET stage = '%s', probability = %f, updated_at = '%s' WHERE id = %d;",
		newStage, newProbability, now, dealID)
	_, err := db.Query("crmdb", query)
	return err
}

func (s *CRMService) SearchNotesFTS(keyword string) ([]Note, error) {
	db := s.crmDB.GetRawDB()
	// Using LIKE / ILIKE or GIN FTS index on content
	query := fmt.Sprintf("SELECT id, customer_id, author, content, created_at FROM crm_notes WHERE content LIKE '%%%s%%';", keyword)
	res, err := db.Query("crmdb", query)
	if err != nil {
		return nil, err
	}

	var notes []Note
	for _, row := range res.Rows {
		if len(row) < 5 {
			continue
		}
		n := Note{
			ID:         toInt64(row[0]),
			CustomerID: toInt64(row[1]),
			Author:     fmt.Sprintf("%v", row[2]),
			Content:    fmt.Sprintf("%v", row[3]),
			CreatedAt:  fmt.Sprintf("%v", row[4]),
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func (s *CRMService) ExecuteRawSQL(query string) (*vaultdb.Result, error) {
	db := s.crmDB.GetRawDB()
	return db.Query("crmdb", query)
}

func (s *CRMService) GetSystemStats() (*SystemStats, error) {
	db := s.crmDB.GetRawDB()

	var customerCount, dealCount int64
	cRes, err := db.Query("crmdb", "SELECT COUNT(*) FROM crm_customers;")
	if err == nil && len(cRes.Rows) > 0 {
		customerCount = toInt64(cRes.Rows[0][0])
	}
	dRes, err := db.Query("crmdb", "SELECT COUNT(*) FROM crm_deals;")
	if err == nil && len(dRes.Rows) > 0 {
		dealCount = toInt64(dRes.Rows[0][0])
	}

	stats := &SystemStats{
		BufferPoolPages: 1024,
		ActiveTx:        0,
		CatalogTables:   4,
		TotalCustomers:  customerCount,
		TotalDeals:      dealCount,
		RaftStatus:      "Leader (Node 1)",
		EngineMode:      "Zero-Alloc / HOT / MVCC",
	}
	return stats, nil
}

func (s *CRMService) calculateLeadScoreWasm(name, company, metadata string) float64 {
	// Simple Lead Scoring UDF logic
	score := 50.0
	if len(company) > 3 {
		score += 15.0
	}
	if len(metadata) > 10 {
		score += 20.0
	}
	return score
}

func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case string:
		var n int64
		fmt.Sscanf(val, "%d", &n)
		return n
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	default:
		return 0
	}
}

func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case string:
		var f float64
		fmt.Sscanf(val, "%f", &f)
		return f
	case float64:
		return val
	case int64:
		return float64(val)
	case int:
		return float64(val)
	default:
		return 0.0
	}
}
