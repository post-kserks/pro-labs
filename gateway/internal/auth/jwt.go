// Package auth implements JWT (HS256) auth with a fixed demo-user table.
// It deliberately avoids third-party libraries so the gateway builds with the
// Go standard library only.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User is an authenticated principal.
type User struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"` // doctor | admin | receptionist
	DoctorID int    `json:"doctor_id,omitempty"`
}

// Claims is the JWT payload.
type Claims struct {
	Sub      string `json:"sub"`  // email
	Name     string `json:"name"`
	Role     string `json:"role"`
	DoctorID int    `json:"doctor_id,omitempty"`
	Exp      int64  `json:"exp"`
	Iat      int64  `json:"iat"`
}

// Signer issues and validates HS256 tokens.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

func NewSigner(secret string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), ttl: ttl}
}

var b64 = base64.RawURLEncoding

// Issue builds a signed JWT for the user.
func (s *Signer) Issue(u User) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	now := time.Now()
	claims := Claims{
		Sub:      u.Email,
		Name:     u.Name,
		Role:     u.Role,
		DoctorID: u.DoctorID,
		Iat:      now.Unix(),
		Exp:      now.Add(s.ttl).Unix(),
	}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signingInput := b64.EncodeToString(hb) + "." + b64.EncodeToString(cb)
	sig := s.sign(signingInput)
	return signingInput + "." + sig, nil
}

func (s *Signer) sign(input string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(input))
	return b64.EncodeToString(mac.Sum(nil))
}

// Parse validates a token and returns its claims.
func (s *Signer) Parse(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token")
	}
	signingInput := parts[0] + "." + parts[1]
	expected := s.sign(signingInput)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, errors.New("bad signature")
	}
	cb, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("bad payload: %w", err)
	}
	var claims Claims
	if err := json.Unmarshal(cb, &claims); err != nil {
		return nil, fmt.Errorf("bad claims: %w", err)
	}
	if time.Now().Unix() > claims.Exp {
		return nil, errors.New("token expired")
	}
	return &claims, nil
}

// UserFromClaims rebuilds a User from validated claims.
func UserFromClaims(c *Claims) User {
	return User{Email: c.Sub, Name: c.Name, Role: c.Role, DoctorID: c.DoctorID}
}
