package api

import (
	"encoding/json"
	"net/http"
)

// apiError is the unified error envelope described in the spec.
type apiError struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// WriteJSON writes a JSON body with the given status.
func WriteJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteError writes the unified error envelope: {"error": {...}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]interface{}{
		"error": apiError{Code: code, Message: message},
	})
}

// decodeJSON reads and decodes a JSON request body.
func decodeJSON(r *http.Request, dst interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}
