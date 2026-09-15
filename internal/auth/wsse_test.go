package auth

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"mockcam/internal/config"
)

const wsseEnvelope = `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Header>
    <wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
      <wsse:UsernameToken wsu:Id="UsernameToken-1">
        <wsse:Username>%s</wsse:Username>
        <wsse:Password Type="%s">%s</wsse:Password>
        <wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</wsse:Nonce>
        <wsu:Created>%s</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </s:Header>
  <s:Body><GetDeviceInformation xmlns="http://www.onvif.org/ver10/device/wsdl"/></s:Body>
</s:Envelope>`

func digestToken(user, pass string, created time.Time) (envelope string, tok UsernameToken) {
	nonce := []byte("0123456789abcdef")
	createdStr := created.UTC().Format("2006-01-02T15:04:05.000Z")
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(createdStr))
	h.Write([]byte(pass))
	tok = UsernameToken{
		Username:     user,
		Password:     base64.StdEncoding.EncodeToString(h.Sum(nil)),
		PasswordType: PasswordDigestType,
		Nonce:        base64.StdEncoding.EncodeToString(nonce),
		Created:      createdStr,
	}
	return sprintfEnvelope(tok), tok
}

func sprintfEnvelope(tok UsernameToken) string {
	return fmt.Sprintf(wsseEnvelope, tok.Username, tok.PasswordType, tok.Password, tok.Nonce, tok.Created)
}

func TestParseUsernameToken(t *testing.T) {
	now := time.Now()
	env, want := digestToken("admin", "admin1234", now)
	got, ok := ParseUsernameToken([]byte(env))
	if !ok {
		t.Fatal("token not found")
	}
	if got != want {
		t.Fatalf("parsed token mismatch:\n got %+v\nwant %+v", got, want)
	}

	// Envelopes without a Security header report ok=false.
	plain := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Header><wsa:Action xmlns:wsa="a">x</wsa:Action></s:Header><s:Body><GetScopes/></s:Body></s:Envelope>`
	if _, ok := ParseUsernameToken([]byte(plain)); ok {
		t.Fatal("expected no token")
	}
	if _, ok := ParseUsernameToken([]byte(`<s:Envelope xmlns:s="x"><s:Body><UsernameToken><Username>a</Username></UsernameToken></s:Body></s:Envelope>`)); ok {
		t.Fatal("a UsernameToken inside the Body must be ignored")
	}
	if _, ok := ParseUsernameToken([]byte("<broken")); ok {
		t.Fatal("malformed XML must not yield a token")
	}
}

func TestVerifyUsernameToken(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	_, tok := digestToken("admin", "admin1234", now)
	if !VerifyUsernameToken(tok, "admin", "admin1234", now.Add(30*time.Second)) {
		t.Fatal("valid digest rejected")
	}
	if VerifyUsernameToken(tok, "admin", "wrong", now) {
		t.Fatal("wrong password accepted")
	}
	if VerifyUsernameToken(tok, "other", "admin1234", now) {
		t.Fatal("wrong user accepted")
	}
	if VerifyUsernameToken(tok, "admin", "admin1234", now.Add(UsernameTokenMaxSkew+time.Minute)) {
		t.Fatal("stale token accepted")
	}
	if VerifyUsernameToken(tok, "admin", "admin1234", now.Add(-UsernameTokenMaxSkew-time.Minute)) {
		t.Fatal("token from the future accepted")
	}
	bad := tok
	bad.Nonce = "***"
	if VerifyUsernameToken(bad, "admin", "admin1234", now) {
		t.Fatal("undecodable nonce accepted")
	}
	bad = tok
	bad.Created = "not-a-date"
	if VerifyUsernameToken(bad, "admin", "admin1234", now) {
		t.Fatal("unparsable Created accepted")
	}

	// PasswordText (explicit type and no type at all).
	text := UsernameToken{Username: "admin", Password: "admin1234", PasswordType: PasswordTextType}
	if !VerifyUsernameToken(text, "admin", "admin1234", now) {
		t.Fatal("PasswordText rejected")
	}
	text.PasswordType = ""
	if !VerifyUsernameToken(text, "admin", "admin1234", now) {
		t.Fatal("untyped password rejected")
	}
	text.Password = "nope"
	if VerifyUsernameToken(text, "admin", "admin1234", now) {
		t.Fatal("wrong PasswordText accepted")
	}
	text.PasswordType = "urn:unknown"
	if VerifyUsernameToken(text, "admin", "admin1234", now) {
		t.Fatal("unknown password type accepted")
	}
}

func TestAuthenticatorValidateUsernameToken(t *testing.T) {
	cfgMgr, err := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAuthenticator(cfgMgr)
	now := time.Now()
	_, tok := digestToken("admin", "admin1234", now)
	if !a.ValidateUsernameToken(tok, now) {
		t.Fatal("default credentials rejected")
	}
	_, bad := digestToken("admin", "bad", now)
	if a.ValidateUsernameToken(bad, now) {
		t.Fatal("bad credentials accepted")
	}

	// Disabled auth accepts anything.
	cfg := cfgMgr.Get()
	cfg.Server.AuthType = "none"
	if err := cfgMgr.UpdateServerConfig(cfg.Server); err != nil {
		t.Fatal(err)
	}
	if !a.ValidateUsernameToken(bad, now) {
		t.Fatal("auth disabled should accept")
	}
}
