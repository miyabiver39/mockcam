package rtsp

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/base"

	"mockcam/internal/auth"
)

// fakeValidator is a CredentialValidator with a fixed user/password.
type fakeValidator struct {
	user, pass string
	digest     *auth.DigestManager
}

func newFakeValidator(user, pass string) *fakeValidator {
	return &fakeValidator{user: user, pass: pass, digest: auth.NewDigestManager("MockCam")}
}

func (f *fakeValidator) Realm() string                      { return "MockCam" }
func (f *fakeValidator) DigestManager() *auth.DigestManager { return f.digest }
func (f *fakeValidator) ValidateBasicCredentials(u, p string) bool {
	return u == f.user && p == f.pass
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func requestWithAuth(method base.Method, header string) *base.Request {
	req := &base.Request{Method: method, Header: base.Header{}}
	if header != "" {
		req.Header["Authorization"] = base.HeaderValue{header}
	}
	return req
}

func TestParseBasicAuth(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		wantUser string
		wantPass string
		wantOK   bool
	}{
		{"valid", basicHeader("admin", "admin1234"), "admin", "admin1234", true},
		{"password with colon", basicHeader("admin", "a:b:c"), "admin", "a:b:c", true},
		{"case-insensitive scheme", "BASIC " + base64.StdEncoding.EncodeToString([]byte("u:p")), "u", "p", true},
		{"missing colon must not panic", "Basic " + base64.StdEncoding.EncodeToString([]byte("nocolon")), "", "", false},
		{"invalid base64", "Basic !!!", "", "", false},
		{"wrong scheme", "Digest username=admin", "", "", false},
		{"empty", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user, pass, ok := parseBasicAuth(tc.header)
			if ok != tc.wantOK || user != tc.wantUser || pass != tc.wantPass {
				t.Fatalf("parseBasicAuth(%q) = (%q,%q,%v), want (%q,%q,%v)",
					tc.header, user, pass, ok, tc.wantUser, tc.wantPass, tc.wantOK)
			}
		})
	}
}

func TestAuthorizeRequest_LoopbackPublishBypassesAuth(t *testing.T) {
	v := newFakeValidator("admin", "secret")
	req := requestWithAuth(base.Announce, "")
	if resp := authorizeRequest(req, true, "127.0.0.1:51234", "digest", "admin", "secret", v); resp != nil {
		t.Fatalf("loopback publish should be allowed, got status %d", resp.StatusCode)
	}
	// A loopback *reader* still needs credentials.
	if resp := authorizeRequest(req, false, "127.0.0.1:51234", "digest", "admin", "secret", v); resp == nil {
		t.Fatal("loopback reader without credentials should be rejected")
	}
	// A remote publisher must authenticate too.
	if resp := authorizeRequest(req, true, "192.168.1.20:5000", "digest", "admin", "secret", v); resp == nil {
		t.Fatal("remote publisher without credentials should be rejected")
	}
}

func TestAuthorizeRequest_AuthDisabled(t *testing.T) {
	v := newFakeValidator("admin", "secret")
	for _, mode := range []string{"none", "", "off", "disabled"} {
		if resp := authorizeRequest(requestWithAuth(base.Describe, ""), false, "10.0.0.5:1", mode, "admin", "secret", v); resp != nil {
			t.Fatalf("auth_type=%q should allow anonymous access", mode)
		}
	}
}

func TestAuthorizeRequest_Basic(t *testing.T) {
	v := newFakeValidator("admin", "secret")

	if resp := authorizeRequest(requestWithAuth(base.Describe, basicHeader("admin", "secret")), false, "10.0.0.5:1", "basic", "admin", "secret", v); resp != nil {
		t.Fatalf("valid basic credentials rejected: %d", resp.StatusCode)
	}

	resp := authorizeRequest(requestWithAuth(base.Describe, basicHeader("admin", "wrong")), false, "10.0.0.5:1", "basic", "admin", "secret", v)
	if resp == nil || resp.StatusCode != base.StatusUnauthorized {
		t.Fatalf("invalid basic credentials should yield 401, got %v", resp)
	}
	if got := resp.Header["WWW-Authenticate"]; len(got) != 1 || !strings.HasPrefix(got[0], `Basic realm="MockCam"`) {
		t.Fatalf("basic mode should send a Basic challenge, got %v", got)
	}

	// Basic credentials are also accepted when the server prefers digest.
	if resp := authorizeRequest(requestWithAuth(base.Describe, basicHeader("admin", "secret")), false, "10.0.0.5:1", "digest", "admin", "secret", v); resp != nil {
		t.Fatalf("basic credentials should be accepted in digest mode: %d", resp.StatusCode)
	}
}

func TestAuthorizeRequest_MissingOrUnknownScheme(t *testing.T) {
	v := newFakeValidator("admin", "secret")

	resp := authorizeRequest(requestWithAuth(base.Describe, ""), false, "10.0.0.5:1", "digest", "admin", "secret", v)
	if resp == nil || resp.StatusCode != base.StatusUnauthorized {
		t.Fatalf("missing Authorization should yield 401, got %v", resp)
	}
	if got := resp.Header["WWW-Authenticate"]; len(got) != 1 || !strings.HasPrefix(got[0], "Digest realm=") {
		t.Fatalf("digest mode should send a Digest challenge, got %v", got)
	}

	resp = authorizeRequest(requestWithAuth(base.Describe, "Bearer abc"), false, "10.0.0.5:1", "digest", "admin", "secret", v)
	if resp == nil || resp.StatusCode != base.StatusUnauthorized {
		t.Fatalf("unknown scheme should yield 401, got %v", resp)
	}

	// nil request must be handled gracefully.
	if resp := authorizeRequest(nil, false, "10.0.0.5:1", "digest", "admin", "secret", v); resp == nil {
		t.Fatal("nil request should be rejected when auth is enabled")
	}
}

func TestAuthorizeRequest_Digest(t *testing.T) {
	v := newFakeValidator("admin", "secret")

	// Obtain a nonce from a challenge, then build a valid RFC 2617 response.
	challenge := v.DigestManager().ChallengeHeader()
	params := auth.ParseDigestAuthorization("Digest " + strings.TrimPrefix(challenge, "Digest "))
	nonce := params["nonce"]
	uri := "rtsp://localhost:8554/live/Profile_1"
	ha1 := auth.MD5Hex("admin:MockCam:secret")
	ha2 := auth.MD5Hex("DESCRIBE:" + uri)
	response := auth.MD5Hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	header := fmt.Sprintf(`Digest username="admin", realm="MockCam", nonce="%s", uri="%s", response="%s"`, nonce, uri, response)

	if resp := authorizeRequest(requestWithAuth(base.Describe, header), false, "10.0.0.5:1", "digest", "admin", "secret", v); resp != nil {
		t.Fatalf("valid digest rejected: %d", resp.StatusCode)
	}

	// Same header for a different method must fail (HA2 includes the method).
	if resp := authorizeRequest(requestWithAuth(base.Setup, header), false, "10.0.0.5:1", "digest", "admin", "secret", v); resp == nil {
		t.Fatal("digest computed for DESCRIBE must not authorize SETUP")
	}

	// Wrong password must fail.
	if resp := authorizeRequest(requestWithAuth(base.Describe, header), false, "10.0.0.5:1", "digest", "admin", "other", v); resp == nil {
		t.Fatal("digest with wrong password must be rejected")
	}
}
