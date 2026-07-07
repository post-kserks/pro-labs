package protocol

import (
	"fmt"
	"strconv"
	"strings"
)

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

func parseMajorVersion(version string) (int, error) {
	parts := strings.SplitN(version, ".", 2)
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty version string")
	}
	return strconv.Atoi(parts[0])
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
