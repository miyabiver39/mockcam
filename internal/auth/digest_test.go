package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mockcam/internal/config"
)

func TestParseDigestAuthorization(t *testing.T) {
	params := ParseDigestAuthorization(`Digest username="admin", realm="MockCam", nonce="abc", uri="/x", response="ff", qop=auth, nc=00000001, cnonce="zz", algorithm=MD5`)
	want := map[string]string{
		"username": "admin", "realm": "MockCam", "nonce": "abc", "uri": "/x",
		"response": "ff", "qop": "auth", "nc": "00000001", "cnonce": "zz", "algorithm": "MD5",
	}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("%s = %q, want %q", k, params[k], v)
		}
	}
	if len(ParseDigestAuthorization("Basic abc")) != 0 {
		t.Fatal("non-digest header should yield no params")
	}
	if len(ParseDigestAuthorization("")) != 0 {
		t.Fatal("empty header")
	}
	// Case-insensitive scheme.
	if ParseDigestAuthorization(`DIGEST username="u"`)["username"] != "u" {
		t.Fatal("scheme should be case-insensitive")
	}
}

func TestNonceLifecycle(t *testing.T) {
	dm := NewDigestManager("")
	if dm.realm != "MockCam" {
		t.Fatal("empty realm should default to MockCam")
	}
	nonce := dm.GenerateNonce()
	if len(nonce) != 32 {
		t.Fatalf("nonce %q should be 16 random bytes hex-encoded", nonce)
	}
	if !dm.ValidateNonce(nonce) {
		t.Fatal("fresh nonce should validate")
	}
	if dm.ValidateNonce("unknown") {
		t.Fatal("unknown nonce should be rejected")
	}
	// Expire the nonce by back-dating it.
	dm.mu.Lock()
	dm.nonces[nonce] = time.Now().Add(-10 * time.Minute)
	dm.mu.Unlock()
	if dm.ValidateNonce(nonce) {
		t.Fatal("expired nonce should be rejected")
	}
	if _, still := dm.nonces[nonce]; still {
		t.Fatal("expired nonce should be removed")
	}
}

func TestChallengeHeaderRegistersNonce(t *testing.T) {
	dm := NewDigestManager("Realm1")
	h := dm.ChallengeHeader()
	if !strings.HasPrefix(h, `Digest realm="Realm1", nonce="`) || !strings.Contains(h, `qop="auth"`) {
		t.Fatalf("challenge = %q", h)
	}
	nonce := ParseDigestAuthorization("Digest " + strings.TrimPrefix(h, "Digest "))["nonce"]
	if !dm.ValidateNonce(nonce) {
		t.Fatal("nonce from the challenge should be valid")
	}
}

func TestValidateDigestRejections(t *testing.T) {
	dm := NewDigestManager("MockCam")
	nonce := dm.GenerateNonce()
	ha1 := MD5Hex("admin:MockCam:pw")
	ha2 := MD5Hex("GET:/u")
	good := map[string]string{
		"username": "admin", "realm": "MockCam", "nonce": nonce, "uri": "/u",
		"response": MD5Hex(ha1 + ":" + nonce + ":" + ha2),
	}
	if !dm.ValidateDigest("GET", good, "admin", "pw") {
		t.Fatal("valid digest (no qop) rejected")
	}

	mutate := func(k, v string) map[string]string {
		m := map[string]string{}
		for kk, vv := range good {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	if dm.ValidateDigest("GET", mutate("username", "other"), "admin", "pw") {
		t.Fatal("wrong username accepted")
	}
	if dm.ValidateDigest("GET", mutate("realm", "Other"), "admin", "pw") {
		t.Fatal("wrong realm accepted")
	}
	if dm.ValidateDigest("GET", mutate("nonce", "bogus"), "admin", "pw") {
		t.Fatal("unknown nonce accepted")
	}
	if dm.ValidateDigest("POST", good, "admin", "pw") {
		t.Fatal("digest for GET accepted for POST")
	}
	if dm.ValidateDigest("GET", good, "admin", "other") {
		t.Fatal("wrong password accepted")
	}
	// Response comparison is case-insensitive (hex).
	if !dm.ValidateDigest("GET", mutate("response", strings.ToUpper(good["response"])), "admin", "pw") {
		t.Fatal("uppercase hex response should be accepted")
	}
}

func TestIsAuthEnabled(t *testing.T) {
	for _, off := range []string{"", "none", "NONE", " off ", "disabled", "false"} {
		if IsAuthEnabled(off) {
			t.Errorf("%q should disable auth", off)
		}
	}
	for _, on := range []string{"digest", "basic", "Digest", "anything"} {
		if !IsAuthEnabled(on) {
			t.Errorf("%q should enable auth", on)
		}
	}
}

func TestMiddlewareAndBasicChallenge(t *testing.T) {
	cfgMgr, err := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAuthenticator(cfgMgr)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	protected := a.Middleware(next)

	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("middleware should block anonymous requests: %d", rec.Code)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "admin1234")
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("middleware should pass valid credentials: %d", rec.Code)
	}

	// Malformed basic payload and unknown schemes are rejected.
	for _, hdr := range []string{"Basic !!!", "Basic " + "bm9jb2xvbg==", "Bearer x"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", hdr)
		rec := httptest.NewRecorder()
		if a.CheckHTTP(rec, req) || rec.Code != 401 {
			t.Errorf("header %q should be rejected", hdr)
		}
	}

	// In basic mode the challenge is Basic; with auth disabled everything passes.
	srv := cfgMgr.Get().Server
	srv.AuthType = "basic"
	_ = cfgMgr.UpdateServerConfig(srv)
	rec = httptest.NewRecorder()
	a.CheckHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic realm=") {
		t.Fatalf("basic challenge expected, got %q", rec.Header().Get("WWW-Authenticate"))
	}

	srv.AuthType = "none"
	_ = cfgMgr.UpdateServerConfig(srv)
	if !a.CheckHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("auth disabled should allow anonymous access")
	}
	if !a.ValidateBasicCredentials("anyone", "anything") {
		t.Fatal("credential validation should pass when auth is disabled")
	}
}
