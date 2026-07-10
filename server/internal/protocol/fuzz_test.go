package protocol

import (
	"encoding/json"
	"testing"
)

// FuzzParse fuzzes the binary protocol parsing to find crashes with random bytes.
func FuzzParse(f *testing.F) {
	// Seed corpus with valid and edge-case inputs
	f.Add([]byte(`{"id":"1","query":"SELECT 1","token":"tok"}`))
	f.Add([]byte(`{"type":"handshake","client_version":"2.0","client_name":"test"}`))
	f.Add([]byte(`{"status":"ok","type":"rows","columns":["id"],"rows":[["1"]]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`""`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"type":"handshake","client_version":"2.0","nonce":"abc","nonce_timestamp":1700000000}`))
	f.Add([]byte(`{"id":"1","query":"SELECT * FROM t WHERE id = $1","params":[42],"database":"testdb","version":"2.0"}`))
	f.Add([]byte(`{"id":"1","query":"SELECT 1","token":"","version":"","params":null,"database":"","as_of":null}`))
	f.Add([]byte(`{"type":"handshake","client_version":"1.0"}`))
	f.Add([]byte(`{"type":"unknown","client_version":"2.0"}`))
	f.Add(make([]byte, 0))
	f.Add(make([]byte, 1024))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Fuzz Request parsing - must not panic
		req, _ := ParseRequest(data)
		_ = req

		// Fuzz Response parsing - must not panic
		resp, _ := ParseResponse(data)
		_ = resp

		// Fuzz Handshake parsing - must not panic
		hs, _ := ParseHandshake(data)
		_ = hs

		// Fuzz direct JSON unmarshal into Request - must not panic
		var rawReq Request
		_ = json.Unmarshal(data, &rawReq)

		// Fuzz direct JSON unmarshal into Response - must not panic
		var rawResp Response
		_ = json.Unmarshal(data, &rawResp)

		// Fuzz ValidateHandshakeRequest with whatever we can construct
		var hsReq HandshakeRequest
		if err := json.Unmarshal(data, &hsReq); err == nil {
			_ = ValidateHandshakeRequest(hsReq)
		}

		// Fuzz CheckVersionCompatibility
		_ = CheckVersionCompatibility(string(data))

		// Fuzz ValidateNonce
		_ = ValidateNonce(string(data), 1700000000)

		// Fuzz parseMajorVersion
		_, _ = parseMajorVersion(string(data))
	})
}

// FuzzParseRequest specifically fuzzes request parsing with various malformed inputs.
func FuzzParseRequest(f *testing.F) {
	f.Add([]byte(`{"id":"","query":"","token":""}`))
	f.Add([]byte(`{"id":"x"}`))
	f.Add([]byte(`{"query":"SELECT 1"}`))
	f.Add([]byte(`{"id":"1","query":"SELECT 1","params":[1,"two",true,null,{}],"database":"db","version":"2.0","as_of":12345,"isolation":"serializable"}`))
	f.Add([]byte(`{"id":"1","query":"SELECT 1","token":"` + string(make([]byte, 10000)) + `"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		req, err := ParseRequest(data)
		if err != nil {
			return
		}
		// If parsing succeeded, round-trip should work
		out, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("valid request failed to marshal: %v", err)
		}
		var req2 Request
		if err := json.Unmarshal(out, &req2); err != nil {
			t.Fatalf("round-trip failed: %v", err)
		}
	})
}

// FuzzParseHandshake specifically fuzzes handshake parsing.
func FuzzParseHandshake(f *testing.F) {
	f.Add([]byte(`{"type":"handshake","client_version":"2.0","client_name":"test","supported_features":["params"],"nonce":"abc","nonce_timestamp":1700000000}`))
	f.Add([]byte(`{"type":"handshake","client_version":"3.0"}`))
	f.Add([]byte(`{"type":"not_handshake","client_version":"2.0"}`))
	f.Add([]byte(`{"client_version":"2.0"}`))
	f.Add([]byte(`{"type":"handshake"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		hs, err := ParseHandshake(data)
		if err != nil {
			return
		}
		// If parsing succeeded, it should be a valid handshake
		if err := ValidateHandshakeRequest(hs); err != nil {
			t.Fatalf("ParseHandshake returned valid but ValidateHandshakeRequest failed: %v", err)
		}
	})
}

// FuzzParseResponse specifically fuzzes response parsing.
func FuzzParseResponse(f *testing.F) {
	f.Add([]byte(`{"id":"1","status":"ok","type":"rows","columns":["id","name"],"rows":[["1","Alice"],["2","Bob"]],"affected":0,"message":"","duration_ms":42}`))
	f.Add([]byte(`{"id":"1","status":"error","type":"error","message":"table not found"}`))
	f.Add([]byte(`{"id":"1","status":"ok","type":"affected","affected":5}`))
	f.Add([]byte(`{"id":"1","status":"ok","type":"rows","columns":null,"rows":null}`))
	f.Add([]byte(`{"id":"1","status":"ok","type":"rows","columns":[],"rows":[],"encryption_meta":{"key_id":"k1","algorithm":"aes-256-gcm"}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		resp, err := ParseResponse(data)
		if err != nil {
			return
		}
		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("valid response failed to marshal: %v", err)
		}
		var resp2 Response
		if err := json.Unmarshal(out, &resp2); err != nil {
			t.Fatalf("round-trip failed: %v", err)
		}
	})
}
