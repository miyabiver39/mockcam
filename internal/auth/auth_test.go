package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mockcam/internal/config"
)

func TestAuthBasicAndDigest(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "settings.json")
	cfgMgr, err := config.NewManager(cfgPath)
	if err != nil {
		t.Fatalf("failed to init config: %v", err)
	}

	auth := NewAuthenticator(cfgMgr)

	// Test 1: Unauthorized request returns 401
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/test", nil)
	if auth.CheckHTTP(rec, req) {
		t.Fatal("expected request without auth header to fail")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if challenge == "" {
		t.Fatal("expected WWW-Authenticate header")
	}

	// Test 2: Valid Basic Auth
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/test", nil)
	validBasic := base64.StdEncoding.EncodeToString([]byte("admin:admin1234"))
	req.Header.Set("Authorization", "Basic "+validBasic)
	if !auth.CheckHTTP(rec, req) {
		t.Fatal("expected valid basic auth to succeed")
	}

	// Test 3: Invalid Basic Auth
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/test", nil)
	invalidBasic := base64.StdEncoding.EncodeToString([]byte("admin:wrongpass"))
	req.Header.Set("Authorization", "Basic "+invalidBasic)
	if auth.CheckHTTP(rec, req) {
		t.Fatal("expected invalid basic auth to fail")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	// Test 4: Valid Digest Auth
	nonce := auth.DigestManager().GenerateNonce()
	method := "GET"
	uri := "/api/test"
	qop := "auth"
	nc := "00000001"
	cnonce := "abcdef123456"

	ha1 := MD5Hex(fmt.Sprintf("%s:%s:%s", "admin", auth.Realm(), "admin1234"))
	ha2 := MD5Hex(fmt.Sprintf("%s:%s", method, uri))
	expectedResp := MD5Hex(fmt.Sprintf("%s:%s:%s:%s:%s:%s", ha1, nonce, nc, cnonce, qop, ha2))

	digestHeader := fmt.Sprintf(
		`Digest username="admin", realm="%s", nonce="%s", uri="%s", response="%s", qop=%s, nc=%s, cnonce="%s"`,
		auth.Realm(), nonce, uri, expectedResp, qop, nc, cnonce,
	)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(method, uri, nil)
	req.Header.Set("Authorization", digestHeader)
	if !auth.CheckHTTP(rec, req) {
		t.Fatal("expected valid digest auth to succeed")
	}
}
