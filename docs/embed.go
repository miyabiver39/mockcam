// Package docs embeds the machine-readable API documentation so the binary
// can serve it (/openapi.yaml, /api/docs) without any external files.
package docs

import _ "embed"

// OpenAPI is the OpenAPI 3.1 description of the management API.
//
//go:embed openapi.yaml
var OpenAPI []byte
