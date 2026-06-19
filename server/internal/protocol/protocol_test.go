package protocol

import (
	"encoding/json"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		req  Request
	}{
		{
			name: "basic query",
			req: Request{
				ID:    "test-1",
				Query: "SELECT * FROM users",
				Token: "vdb_sk_test123",
			},
		},
		{
			name: "empty query",
			req: Request{
				ID:    "test-2",
				Query: "",
				Token: "token",
			},
		},
		{
			name: "special characters",
			req: Request{
				ID:    "test-3",
				Query: "SELECT 'hello\"world' FROM t",
				Token: "token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			var decoded Request
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if decoded.ID != tt.req.ID {
				t.Errorf("ID = %q, want %q", decoded.ID, tt.req.ID)
			}
			if decoded.Query != tt.req.Query {
				t.Errorf("Query = %q, want %q", decoded.Query, tt.req.Query)
			}
			if decoded.Token != tt.req.Token {
				t.Errorf("Token = %q, want %q", decoded.Token, tt.req.Token)
			}
		})
	}
}

func TestResponseRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		resp Response
	}{
		{
			name: "ok response",
			resp: Response{
				ID:       "test-1",
				Status:   "ok",
				Type:     "rows",
				Columns:  []string{"id", "name"},
				Rows:     [][]string{{"1", "Alice"}, {"2", "Bob"}},
				Affected: 0,
				Message:  "",
			},
		},
		{
			name: "error response",
			resp: Response{
				ID:      "test-2",
				Status:  "error",
				Type:    "error",
				Message: "table not found",
			},
		},
		{
			name: "affected rows",
			resp: Response{
				ID:       "test-3",
				Status:   "ok",
				Type:     "affected",
				Affected: 5,
				Message:  "5 rows deleted",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.resp)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			var decoded Response
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if decoded.ID != tt.resp.ID {
				t.Errorf("ID = %q, want %q", decoded.ID, tt.resp.ID)
			}
			if decoded.Status != tt.resp.Status {
				t.Errorf("Status = %q, want %q", decoded.Status, tt.resp.Status)
			}
			if decoded.Type != tt.resp.Type {
				t.Errorf("Type = %q, want %q", decoded.Type, tt.resp.Type)
			}
			if decoded.Message != tt.resp.Message {
				t.Errorf("Message = %q, want %q", decoded.Message, tt.resp.Message)
			}
			if decoded.Affected != tt.resp.Affected {
				t.Errorf("Affected = %d, want %d", decoded.Affected, tt.resp.Affected)
			}
			if len(decoded.Columns) != len(tt.resp.Columns) {
				t.Errorf("Columns len = %d, want %d", len(decoded.Columns), len(tt.resp.Columns))
			}
			if len(decoded.Rows) != len(tt.resp.Rows) {
				t.Errorf("Rows len = %d, want %d", len(decoded.Rows), len(tt.resp.Rows))
			}
		})
	}
}

func TestRequestJSON(t *testing.T) {
	jsonStr := `{"id":"1","query":"SELECT 1","token":"tok"}`
	var req Request
	if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if req.ID != "1" {
		t.Errorf("ID = %q, want %q", req.ID, "1")
	}
	if req.Query != "SELECT 1" {
		t.Errorf("Query = %q, want %q", req.Query, "SELECT 1")
	}
}

func TestResponseJSON(t *testing.T) {
	resp := Response{
		ID:      "1",
		Status:  "ok",
		Type:    "message",
		Message: "done",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Message != "done" {
		t.Errorf("Message = %q, want %q", decoded.Message, "done")
	}
}
