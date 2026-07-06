package protocol

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
