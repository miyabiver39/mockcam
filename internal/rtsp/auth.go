package rtsp

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/bluenviron/gortsplib/v5/pkg/base"

	"mockcam/internal/auth"
)

// CredentialValidator is the subset of auth.Authenticator used by the RTSP server.
// It is an interface so tests can substitute a fake without touching config files.
type CredentialValidator interface {
	Realm() string
	ValidateBasicCredentials(user, pass string) bool
	DigestManager() *auth.DigestManager
}

// parseBasicAuth decodes the payload of a "Basic <base64>" Authorization header.
// It returns ok=false when the payload is malformed or lacks a ':' separator.
func parseBasicAuth(header string) (user, pass string, ok bool) {
	if len(header) < 6 || !strings.EqualFold(header[:6], "basic ") {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[6:]))
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// authorizeRequest validates the Authorization header of an RTSP request.
// It returns nil when the request is allowed, or a 401 response otherwise.
//
// Rules:
//   - Publishers connecting from loopback (the internal FFmpeg workers) are always allowed.
//   - When authentication is disabled in configuration, everything is allowed.
//   - Otherwise Basic or Digest credentials must match the configured user/password.
func authorizeRequest(
	req *base.Request,
	isPublish bool,
	remoteAddr string,
	authType, expectedUser, expectedPass string,
	validator CredentialValidator,
) *base.Response {
	if isPublish && isLoopback(remoteAddr) {
		return nil
	}
	if !auth.IsAuthEnabled(authType) {
		return nil
	}

	header := ""
	if req != nil {
		if values := req.Header["Authorization"]; len(values) > 0 {
			header = values[0]
		}
	}
	if header == "" {
		return unauthorizedResponse(authType, validator)
	}

	lower := strings.ToLower(header)
	switch {
	case strings.HasPrefix(lower, "basic "):
		user, pass, ok := parseBasicAuth(header)
		if !ok || !validator.ValidateBasicCredentials(user, pass) {
			return unauthorizedResponse(authType, validator)
		}
		return nil
	case strings.HasPrefix(lower, "digest "):
		params := auth.ParseDigestAuthorization(header)
		method := ""
		if req != nil {
			method = string(req.Method)
		}
		if !validator.DigestManager().ValidateDigest(method, params, expectedUser, expectedPass) {
			return unauthorizedResponse(authType, validator)
		}
		return nil
	default:
		return unauthorizedResponse(authType, validator)
	}
}

// unauthorizedResponse builds a 401 response carrying the appropriate challenge.
func unauthorizedResponse(authType string, validator CredentialValidator) *base.Response {
	var challenge string
	if strings.EqualFold(strings.TrimSpace(authType), "basic") {
		challenge = fmt.Sprintf(`Basic realm="%s"`, validator.Realm())
	} else {
		challenge = validator.DigestManager().ChallengeHeader()
	}
	return &base.Response{
		StatusCode: base.StatusUnauthorized,
		Header: base.Header{
			"WWW-Authenticate": base.HeaderValue{challenge},
		},
	}
}
