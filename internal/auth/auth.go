package auth

import (
	"encoding/base64"
	"net/http"
	"strings"

	"mockcam/internal/config"
)

// Authenticator handles Basic and Digest authentication for HTTP and RTSP.
type Authenticator struct {
	cfgManager *config.Manager
	digestMgr  *DigestManager
	realm      string
}

// NewAuthenticator creates an Authenticator backed by config.Manager.
func NewAuthenticator(cfgMgr *config.Manager) *Authenticator {
	realm := "MockCam"
	return &Authenticator{
		cfgManager: cfgMgr,
		digestMgr:  NewDigestManager(realm),
		realm:      realm,
	}
}

// Realm returns the authentication realm.
func (a *Authenticator) Realm() string {
	return a.realm
}

// DigestManager returns the underlying DigestManager.
func (a *Authenticator) DigestManager() *DigestManager {
	return a.digestMgr
}

// IsAuthEnabled checks if authentication is enabled for given auth_type.
func IsAuthEnabled(authType string) bool {
	norm := strings.ToLower(strings.TrimSpace(authType))
	return norm != "" && norm != "none" && norm != "disabled" && norm != "off" && norm != "false"
}

// ValidateBasicCredentials checks raw username and password.
func (a *Authenticator) ValidateBasicCredentials(user, pass string) bool {
	cfg := a.cfgManager.Get()
	if !IsAuthEnabled(cfg.Server.AuthType) {
		return true
	}
	return user == cfg.Server.AuthUser && pass == cfg.Server.AuthPass
}

// CheckHTTP validates an incoming HTTP request.
// Returns true if authenticated, or false with WWW-Authenticate header sent and 401 set.
func (a *Authenticator) CheckHTTP(w http.ResponseWriter, r *http.Request) bool {
	cfg := a.cfgManager.Get()
	if !IsAuthEnabled(cfg.Server.AuthType) {
		return true
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		a.sendUnauthorized(w)
		return false
	}

	if strings.HasPrefix(strings.ToLower(authHeader), "basic ") {
		payload := strings.TrimSpace(authHeader[6:])
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			a.sendUnauthorized(w)
			return false
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || !a.ValidateBasicCredentials(parts[0], parts[1]) {
			a.sendUnauthorized(w)
			return false
		}
		return true
	}

	if strings.HasPrefix(strings.ToLower(authHeader), "digest ") {
		params := ParseDigestAuthorization(authHeader)
		if !a.digestMgr.ValidateDigest(r.Method, params, cfg.Server.AuthUser, cfg.Server.AuthPass) {
			a.sendUnauthorized(w)
			return false
		}
		return true
	}

	a.sendUnauthorized(w)
	return false
}

func (a *Authenticator) sendUnauthorized(w http.ResponseWriter) {
	cfg := a.cfgManager.Get()
	if cfg.Server.AuthType == "basic" {
		w.Header().Set("WWW-Authenticate", `Basic realm="`+a.realm+`"`)
	} else {
		w.Header().Set("WWW-Authenticate", a.digestMgr.ChallengeHeader())
	}
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("401 Unauthorized\n"))
}

// Middleware wraps an http.Handler with authentication.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.CheckHTTP(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}
