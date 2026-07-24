package crm_test

import (
	"testing"

	"vaultdb/internal/crm"
)

func TestVaultCRMIntegration(t *testing.T) {
	dir := t.TempDir()

	crmDB, err := crm.InitCRMDatabase(dir)
	if err != nil {
		t.Fatalf("InitCRMDatabase failed: %v", err)
	}
	defer crmDB.Close()

	service := crm.NewCRMService(crmDB)

	// 1. Check Initial Seed Customers
	custs, err := service.GetCustomers()
	if err != nil {
		t.Fatalf("GetCustomers failed: %v", err)
	}
	if len(custs) == 0 {
		t.Fatalf("expected seeded customers, got 0")
	}

	// 2. Add New Customer (Testing DML & WASM Lead Scoring)
	newCust, err := service.AddCustomer("Михаил Соколов", "mikhail@sokolov.ru", "Sokolov Tech", "Active", `{"tier":"Gold"}`)
	if err != nil {
		t.Fatalf("AddCustomer failed: %v", err)
	}
	if newCust.Score <= 0 {
		t.Fatalf("expected positive score, got %f", newCust.Score)
	}

	// 3. Check Deals & HOT Update
	deals, err := service.GetDeals()
	if err != nil {
		t.Fatalf("GetDeals failed: %v", err)
	}
	if len(deals) == 0 {
		t.Fatalf("expected seeded deals, got 0")
	}

	// Perform HOT Update on Deal stage
	err = service.UpdateDealStage(deals[0].ID, "Closed Won", 1.0)
	if err != nil {
		t.Fatalf("UpdateDealStage failed: %v", err)
	}

	// 4. Test GIN Full-Text Search
	notes, err := service.SearchNotesFTS("Raft")
	if err != nil {
		t.Fatalf("SearchNotesFTS failed: %v", err)
	}
	if len(notes) == 0 {
		t.Fatalf("expected note matching 'Raft', got 0")
	}

	// 5. Test Interactive SQL Console
	res, err := service.ExecuteRawSQL("SELECT COUNT(*) FROM crm_customers;")
	if err != nil {
		t.Fatalf("ExecuteRawSQL failed: %v", err)
	}
	if res == nil || len(res.Rows) == 0 {
		t.Fatalf("expected SQL query result, got nil/empty")
	}

	// 6. Test System Stats
	stats, err := service.GetSystemStats()
	if err != nil {
		t.Fatalf("GetSystemStats failed: %v", err)
	}
	if stats.TotalCustomers <= 0 {
		t.Fatalf("expected positive customer count, got %d", stats.TotalCustomers)
	}
}
