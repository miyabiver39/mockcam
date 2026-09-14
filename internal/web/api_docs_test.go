package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"mockcam/docs"
	"mockcam/internal/config"
)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

func TestOpenAPIDocumentIsValidAndCoversRoutes(t *testing.T) {
	var spec struct {
		OpenAPI string `yaml:"openapi"`
		Info    struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("openapi.yaml is not valid YAML: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatalf("openapi version = %q", spec.OpenAPI)
	}
	if spec.Info.Version != config.AppVersion {
		t.Fatalf("info.version %q must match AppVersion %q", spec.Info.Version, config.AppVersion)
	}
	if len(spec.Paths) < 15 {
		t.Fatalf("suspiciously few paths: %d", len(spec.Paths))
	}

	// Every documented path/method must be handled by the router: a request
	// to the concrete path must not produce 404 or 405 for a documented method.
	e := newEnv(t)
	for path, ops := range spec.Paths {
		concrete := pathParam.ReplaceAllString(path, "Profile_1")
		for method := range ops {
			m := strings.ToUpper(method)
			if m == "PARAMETERS" {
				continue
			}
			if path == "/ws" || path == "/mcp" || path == "/api/mjpeg/{token}" || path == "/api/audio/timesignal" {
				continue // streaming/upgrade/mounted endpoints are covered by dedicated tests
			}
			req := httptest.NewRequest(m, concrete, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.mux.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound && !strings.Contains(rec.Body.String(), "not found") {
				t.Errorf("%s %s is documented but not routed (404)", m, path)
			}
			if rec.Code == http.StatusMethodNotAllowed {
				t.Errorf("%s %s is documented but the handler rejects the method", m, path)
			}
		}
	}

	// Conversely, every route the API registers must be documented.
	for _, route := range []string{
		"/api/status", "/api/config", "/api/config/reset", "/api/profiles", "/api/profiles/{token}",
		"/api/snapshot/{token}", "/api/mjpeg/{token}", "/api/audio/timesignal", "/api/ptz",
		"/api/ptz/presets", "/api/clients", "/api/logs", "/api/diagnostics/export", "/api/licenses",
		"/api/docs", "/openapi.yaml", "/ws",
	} {
		if _, ok := spec.Paths[route]; !ok {
			t.Errorf("route %s is not documented in openapi.yaml", route)
		}
	}
}

func TestOpenAPIAndDocsEndpoints(t *testing.T) {
	e := newEnv(t)

	rec := e.do(t, "GET", "/openapi.yaml", "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "yaml") || !strings.HasPrefix(rec.Body.String(), "openapi: 3.1") {
		t.Fatalf("openapi: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := e.do(t, "HEAD", "/openapi.yaml", ""); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD openapi: %d", rec.Code)
	}

	rec = e.do(t, "GET", "/api/docs", "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("docs: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"@scalar/api-reference", "url: '/openapi.yaml'", "createApiReference"} {
		if !strings.Contains(body, want) {
			t.Errorf("docs page missing %q", want)
		}
	}
	if e.do(t, "POST", "/api/docs", "").Code != 405 {
		t.Fatal("POST should be 405")
	}
}
