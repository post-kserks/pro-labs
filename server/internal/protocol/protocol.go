package protocol

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GenerateRequestID returns a cryptographically random 32-char hex string
// suitable for use as a request identifier.
func GenerateRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

const (
	ProtocolV2        = "2.0"
	ServerName        = "VaultDB"
	FeatureParams     = "params"
	FeatureDatabase   = "database"
	FeatureAsOf       = "as_of"
)

// HandshakeRequest is the first message from a v2 client.
type HandshakeRequest struct {
	Type              string   `json:"type"`              // "handshake"
	ClientVersion     string   `json:"client_version"`    // e.g. "2.0"
	ClientName        string   `json:"client_name"`       // e.g. "vaultdb-go-client"
	SupportedFeatures []string `json:"supported_features"` // client features
	Nonce             string   `json:"nonce"`              // anti-replay nonce (RFC 9564)
	NonceTimestamp    int64    `json:"nonce_timestamp"`    // unix timestamp of nonce creation
}

// HandshakeResponse is the server's handshake reply.
type HandshakeResponse struct {
	Type              string   `json:"type"`              // "handshake"
	ProtocolVersion   string   `json:"protocol_version"`  // "2.0"
	Server            string   `json:"server"`            // "VaultDB"
	ServerVersion     string   `json:"server_version"`    // e.g. "2.0.0"
	SupportedFeatures []string `json:"supported_features"` // server features
}

// ServerFeatures returns the features supported by this server.
func ServerFeatures() []string {
	return []string{FeatureParams, FeatureDatabase, FeatureAsOf}
}

// ValidateHandshakeRequest checks that the request has required fields and the
// client version is compatible (major version must match).
func ValidateHandshakeRequest(req HandshakeRequest) error {
	if req.Type == "" {
		return fmt.Errorf("type is required")
	}
	if req.Type != "handshake" {
		return fmt.Errorf("type must be \"handshake\", got %q", req.Type)
	}
	if req.ClientVersion == "" {
		return fmt.Errorf("client_version is required")
	}
	return nil
}

// CheckVersionCompatibility rejects the handshake if the client major version
// does not match the server major version. Returns nil if compatible.
func CheckVersionCompatibility(clientVersion string) error {
	clientMajor, err := parseMajorVersion(clientVersion)
	if err != nil {
		return fmt.Errorf("invalid client_version %q: %w", clientVersion, err)
	}
	serverMajor, err := parseMajorVersion(ProtocolV2)
	if err != nil {
		return fmt.Errorf("internal error: invalid server protocol version: %w", err)
	}
	if clientMajor != serverMajor {
		return fmt.Errorf("version mismatch: client %q (major %d) is incompatible with server protocol %q (major %d)",
			clientVersion, clientMajor, ProtocolV2, serverMajor)
	}
	return nil
}

// NonceMaxAge is the maximum age of a handshake nonce before rejection.
const NonceMaxAge = 30 * time.Second

// ValidateNonce checks that the handshake nonce is present and not older than NonceMaxAge.
func ValidateNonce(nonce string, nonceTimestamp int64) error {
	if nonce == "" {
		return fmt.Errorf("nonce is required for anti-replay protection")
	}
	if nonceTimestamp <= 0 {
		return fmt.Errorf("nonce_timestamp is required")
	}
	nonceTime := time.Unix(nonceTimestamp, 0)
	if time.Since(nonceTime) > NonceMaxAge {
		return fmt.Errorf("nonce expired (older than %v)", NonceMaxAge)
	}
	return nil
}

func parseMajorVersion(version string) (int, error) {
	parts := strings.SplitN(version, ".", 2)
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty version string")
	}
	return strconv.Atoi(parts[0])
}

// ParseRequest parses raw bytes into a Request, returning an error for invalid input.
// It handles JSON unmarshaling and basic validation.
func ParseRequest(data []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return req, fmt.Errorf("invalid request JSON: %w", err)
	}
	return req, nil
}

// ParseResponse parses raw bytes into a Response.
func ParseResponse(data []byte) (Response, error) {
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, fmt.Errorf("invalid response JSON: %w", err)
	}
	return resp, nil
}

// ParseHandshake parses raw bytes into a HandshakeRequest with validation.
func ParseHandshake(data []byte) (HandshakeRequest, error) {
	var req HandshakeRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return req, fmt.Errorf("invalid handshake JSON: %w", err)
	}
	if err := ValidateHandshakeRequest(req); err != nil {
		return req, err
	}
	return req, nil
}

type Request struct {
	ID       string        `json:"id"`
	Token    string        `json:"token,omitempty"`
	Query    string        `json:"query"`
	Version  string        `json:"version,omitempty"`   // "2.0" for v2 clients
	Params   []interface{} `json:"params,omitempty"`    // typed parameters
	Database string        `json:"database,omitempty"`
	AsOf     interface{}   `json:"as_of,omitempty"`     // Time Travel
	Isolation string       `json:"isolation,omitempty"`
}

type Response struct {
	ID             string      `json:"id"`
	Status         string      `json:"status"`
	Type           string      `json:"type"`
	Columns        []string    `json:"columns"`
	Rows           [][]string  `json:"rows"`
	Affected       int         `json:"affected"`
	Message        string      `json:"message,omitempty"`
	AsOfNote       string      `json:"as_of_note,omitempty"`
	DurationMs     int64       `json:"duration_ms,omitempty"`
	EncryptionMeta interface{} `json:"encryption_meta,omitempty"`
}
