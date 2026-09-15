package auth

import (
	"bytes"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"strings"
	"time"
)

// WS-Security UsernameToken support (OASIS wss-username-token-profile-1.0).
// ONVIF clients (ONVIF Device Manager, VMS/NVR software, onvif-zeep, ...)
// authenticate SOAP calls with this header rather than with HTTP Basic/Digest.

const (
	// PasswordDigestType is the Type attribute of a hashed WS-UsernameToken password.
	PasswordDigestType = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest"
	// PasswordTextType is the Type attribute of a clear-text WS-UsernameToken password.
	PasswordTextType = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText"

	// UsernameTokenMaxSkew is the tolerated distance between the token's
	// Created timestamp and the server clock. Clients are expected to sync
	// with GetSystemDateAndTime first, which is why that call is unauthenticated.
	UsernameTokenMaxSkew = 5 * time.Minute
)

// UsernameToken is the parsed wsse:UsernameToken of a SOAP request.
type UsernameToken struct {
	Username     string
	Password     string // digest (base64) or clear text depending on PasswordType
	PasswordType string
	Nonce        string // base64
	Created      string // xs:dateTime (UTC)
}

// ParseUsernameToken extracts the first wsse:UsernameToken found in the SOAP
// Header of envelope. ok is false when the header carries no such token.
// Namespaces are matched by local name only, which keeps the parser tolerant
// of the prefix conventions used by different client stacks.
func ParseUsernameToken(envelope []byte) (tok UsernameToken, ok bool) {
	dec := xml.NewDecoder(bytes.NewReader(envelope))
	inHeader, inToken := false, false
	var field string
	for {
		t, err := dec.Token()
		if err != nil {
			return UsernameToken{}, false
		}
		switch e := t.(type) {
		case xml.StartElement:
			switch {
			case e.Name.Local == "Body":
				return UsernameToken{}, false
			case e.Name.Local == "Header":
				inHeader = true
			case inHeader && !inToken && e.Name.Local == "UsernameToken":
				inToken = true
			case inToken:
				field = e.Name.Local
				if field == "Password" {
					for _, a := range e.Attr {
						if a.Name.Local == "Type" {
							tok.PasswordType = a.Value
						}
					}
				}
			}
		case xml.CharData:
			if !inToken || field == "" {
				continue
			}
			v := strings.TrimSpace(string(e))
			switch field {
			case "Username":
				tok.Username += v
			case "Password":
				tok.Password += v
			case "Nonce":
				tok.Nonce += v
			case "Created":
				tok.Created += v
			}
		case xml.EndElement:
			switch {
			case inToken && e.Name.Local == "UsernameToken":
				return tok, true
			case inToken:
				field = ""
			case e.Name.Local == "Header":
				return UsernameToken{}, false
			}
		}
	}
}

// ValidateUsernameToken checks tok against the configured credentials. It
// accepts both PasswordDigest (SHA-1 of nonce+created+password) and
// PasswordText tokens and enforces UsernameTokenMaxSkew on Created. It always
// returns true when authentication is disabled.
func (a *Authenticator) ValidateUsernameToken(tok UsernameToken, now time.Time) bool {
	cfg := a.cfgManager.Get()
	if !IsAuthEnabled(cfg.Server.AuthType) {
		return true
	}
	return VerifyUsernameToken(tok, cfg.Server.AuthUser, cfg.Server.AuthPass, now)
}

// VerifyUsernameToken is the pure verification used by ValidateUsernameToken.
func VerifyUsernameToken(tok UsernameToken, user, pass string, now time.Time) bool {
	if tok.Username != user {
		return false
	}
	switch tok.PasswordType {
	case "", PasswordTextType:
		return subtle.ConstantTimeCompare([]byte(tok.Password), []byte(pass)) == 1
	case PasswordDigestType:
		created, err := parseWSUTime(tok.Created)
		if err != nil {
			return false
		}
		if d := now.Sub(created); d > UsernameTokenMaxSkew || d < -UsernameTokenMaxSkew {
			return false
		}
		nonce, err := base64.StdEncoding.DecodeString(tok.Nonce)
		if err != nil {
			return false
		}
		want, err := base64.StdEncoding.DecodeString(tok.Password)
		if err != nil {
			return false
		}
		h := sha1.New()
		h.Write(nonce)
		h.Write([]byte(tok.Created))
		h.Write([]byte(pass))
		return subtle.ConstantTimeCompare(h.Sum(nil), want) == 1
	default:
		return false
	}
}

// parseWSUTime parses the wsu:Created timestamp (xs:dateTime, UTC, optional
// fractional seconds).
func parseWSUTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Parse(time.RFC3339, s)
}
