package crm

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type Server struct {
	service *CRMService
	mux     *http.ServeMux
}

func NewServer(service *CRMService) *Server {
	s := &Server{
		service: service,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/customers", s.handleCustomers)
	s.mux.HandleFunc("/api/deals", s.handleDeals)
	s.mux.HandleFunc("/api/notes/search", s.handleSearchNotes)
	s.mux.HandleFunc("/api/sql", s.handleSQLConsole)
	s.mux.HandleFunc("/api/stats", s.handleStats)
}

func (s *Server) handleCustomers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		customers, err := s.service.GetCustomers()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(customers)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Company  string `json:"company"`
			Status   string `json:"status"`
			Metadata string `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cust, err := s.service.AddCustomer(req.Name, req.Email, req.Company, req.Status, req.Metadata)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cust)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleDeals(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		deals, err := s.service.GetDeals()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(deals)
		return
	}

	if r.Method == http.MethodPut {
		var req struct {
			ID          int64   `json:"id"`
			Stage       string  `json:"stage"`
			Probability float64 `json:"probability"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.service.UpdateDealStage(req.ID, req.Stage, req.Probability); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "hot_update": true})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleSearchNotes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query().Get("q")
	notes, err := s.service.SearchNotesFTS(q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(notes)
}

func (s *Server) handleSQLConsole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	res, err := s.service.ExecuteRawSQL(req.Query)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	rowsStr := make([][]string, len(res.Rows))
	for i, row := range res.Rows {
		rowsStr[i] = make([]string, len(row))
		for j, val := range row {
			rowsStr[i][j] = strconv.Quote(jsonToString(val))
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"columns":       res.Columns,
		"rows":          res.Rows,
		"affected_rows": res.Affected,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats, err := s.service.GetSystemStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(stats)
}

func jsonToString(v interface{}) string {
	if v == nil {
		return "NULL"
	}
	return jsonMarshalString(v)
}

func jsonMarshalString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
