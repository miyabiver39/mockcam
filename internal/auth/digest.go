package auth

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DigestManager manages nonces and validates Digest authentication.
type DigestManager struct {
	realm  string
	nonces map[string]time.Time
	mu     sync.Mutex
}

// NewDigestManager creates a new DigestManager for the given realm.
func NewDigestManager(realm string) *DigestManager {
	if realm == "" {
		realm = "MockCam"
	}
	dm := &DigestManager{
		realm:  realm,
		nonces: make(map[string]time.Time),
	}
	// Periodically clean up expired nonces
	go dm.cleanupLoop()
	return dm
}

func (dm *DigestManager) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	for range ticker.C {
		dm.mu.Lock()
		now := time.Now()
		for nonce, created := range dm.nonces {
			if now.Sub(created) > 5*time.Minute {
				delete(dm.nonces, nonce)
			}
		}
		dm.mu.Unlock()
	}
}

// GenerateNonce creates and records a new cryptographic nonce.
func (dm *DigestManager) GenerateNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	nonce := hex.EncodeToString(b)

	dm.mu.Lock()
	dm.nonces[nonce] = time.Now()
	dm.mu.Unlock()

	return nonce
}

// ValidateNonce checks if a nonce is valid.
func (dm *DigestManager) ValidateNonce(nonce string) bool {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	created, exists := dm.nonces[nonce]
	if !exists {
		return false
	}
	if time.Since(created) > 5*time.Minute {
		delete(dm.nonces, nonce)
		return false
	}
	return true
}

// ParseDigestAuthorization parses key-value pairs from an Authorization header.
func ParseDigestAuthorization(header string) map[string]string {
	params := make(map[string]string)
	if !strings.HasPrefix(strings.ToLower(header), "digest ") {
		return params
	}

	raw := strings.TrimSpace(header[7:])
	parts := strings.Split(raw, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			k := strings.TrimSpace(kv[0])
			v := strings.Trim(strings.TrimSpace(kv[1]), `"`)
			params[k] = v
		}
	}
	return params
}

// MD5Hex returns the hex-encoded MD5 hash of input.
func MD5Hex(input string) string {
	h := md5.Sum([]byte(input))
	return hex.EncodeToString(h[:])
}

// ValidateDigest validates a digest response against expected credentials.
func (dm *DigestManager) ValidateDigest(method string, params map[string]string, expectedUser, expectedPass string) bool {
	username := params["username"]
	realm := params["realm"]
	nonce := params["nonce"]
	uri := params["uri"]
	response := params["response"]
	qop := params["qop"]
	nc := params["nc"]
	cnonce := params["cnonce"]

	if username != expectedUser {
		return false
	}
	if realm != "" && realm != dm.realm {
		return false
	}
	if !dm.ValidateNonce(nonce) {
		return false
	}

	ha1 := MD5Hex(fmt.Sprintf("%s:%s:%s", expectedUser, dm.realm, expectedPass))
	ha2 := MD5Hex(fmt.Sprintf("%s:%s", method, uri))

	var expectedResponse string
	if qop == "auth" || qop == "auth-int" {
		expectedResponse = MD5Hex(fmt.Sprintf("%s:%s:%s:%s:%s:%s", ha1, nonce, nc, cnonce, qop, ha2))
	} else {
		expectedResponse = MD5Hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	}

	return strings.EqualFold(response, expectedResponse)
}

// ChallengeHeader returns the WWW-Authenticate header value for Digest auth.
func (dm *DigestManager) ChallengeHeader() string {
	nonce := dm.GenerateNonce()
	return fmt.Sprintf(`Digest realm="%s", nonce="%s", qop="auth", algorithm=MD5`, dm.realm, nonce)
}
