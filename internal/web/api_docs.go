package web

import (
	"net/http"

	"mockcam/docs"
)

// scalarPage renders the interactive API reference with Scalar, loaded from
// jsDelivr like the other dashboard assets. The spec itself is served from
// the same origin so the page works without CORS configuration.
const scalarPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>MockCam API Reference</title>
  <link rel="icon" type="image/png" href="/favicon.png">
  <style>body{margin:0;background:#0f172a}</style>
</head>
<body>
  <div id="app"></div>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  <script>
    Scalar.createApiReference('#app', {
      url: '/openapi.yaml',
      theme: 'kepler',
      darkMode: true,
      hideDarkModeToggle: false,
      metaData: { title: 'MockCam API Reference' },
      defaultHttpClient: { targetKey: 'shell', clientKey: 'curl' }
    })
  </script>
</body>
</html>
`

// handleOpenAPI serves the embedded OpenAPI document.
func (h *APIHandler) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(docs.OpenAPI)
}

// handleAPIDocs serves the Scalar API reference UI.
func (h *APIHandler) handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(scalarPage))
}
