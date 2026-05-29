package api

import (
	"net/http"

	"medvault-gateway/internal/auth"
)

// Login authenticates a demo user and returns a JWT.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request, _ Params) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	user, ok := auth.Authenticate(req.Email, req.Password)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "неверный email или пароль")
		return
	}
	token, err := h.Signer.Issue(user)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", "failed to issue token")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"token": token,
		"user":  user,
	})
}

// Logout is stateless (the client drops the token); provided for completeness.
func (h *Handler) Logout(w http.ResponseWriter, _ *http.Request, _ Params) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Me returns the currently authenticated user.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request, _ Params) {
	WriteJSON(w, http.StatusOK, currentUser(r))
}
