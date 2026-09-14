// Package licenses is the single source of truth for third-party attribution
// shown in the Web UI (/api/licenses) and documented in README.md.
package licenses

// Component describes one third-party dependency or asset.
type Component struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	License   string `json:"license"`
	Copyright string `json:"copyright,omitempty"`
	URL       string `json:"url"`
	// Kind groups components in the UI: "go", "frontend", "runtime", "tts", "font".
	Kind string `json:"kind"`
	// Notes carries mandatory attribution wording (e.g. CC BY) or usage notes.
	Notes string `json:"notes,omitempty"`
}

// Components lists every third-party component bundled with or required by
// MockCam. Keep this in sync with go.mod, the Dockerfile and index.html.
var Components = []Component{
	// --- Go modules (direct dependencies) ---
	{
		Name: "github.com/bluenviron/gortsplib/v5", Version: "v5.6.5", License: "MIT",
		Copyright: "Copyright (c) 2020 aler9 / bluenviron",
		URL:       "https://github.com/bluenviron/gortsplib", Kind: "go",
	},
	{
		Name: "github.com/gorilla/websocket", Version: "v1.5.3", License: "BSD-2-Clause",
		Copyright: "Copyright (c) 2013 The Gorilla WebSocket Authors",
		URL:       "https://github.com/gorilla/websocket", Kind: "go",
	},
	{
		Name: "github.com/google/uuid", Version: "v1.6.0", License: "BSD-3-Clause",
		Copyright: "Copyright (c) 2009,2014 Google Inc.",
		URL:       "https://github.com/google/uuid", Kind: "go",
	},
	{
		Name: "github.com/pion/rtp", Version: "v1.10.5", License: "MIT",
		Copyright: "Copyright (c) 2023 The Pion community",
		URL:       "https://github.com/pion/rtp", Kind: "go",
	},
	{
		Name: "github.com/pion/rtcp", Version: "v1.2.17", License: "MIT",
		Copyright: "Copyright (c) 2023 The Pion community",
		URL:       "https://github.com/pion/rtcp", Kind: "go",
	},
	{
		Name: "github.com/modelcontextprotocol/go-sdk", Version: "v1.8.0", License: "MIT",
		Copyright: "Copyright 2025 The Go MCP SDK Authors",
		URL:       "https://github.com/modelcontextprotocol/go-sdk", Kind: "go",
		Notes: "Model Context Protocol server (/mcp and -mcp-stdio).",
	},
	// --- Go modules (indirect, pulled in by gortsplib and the MCP SDK) ---
	{
		Name: "github.com/google/jsonschema-go", License: "MIT",
		Copyright: "Copyright 2025 The Go JSON Schema Authors",
		URL:       "https://github.com/google/jsonschema-go", Kind: "go",
	},
	{
		Name: "github.com/segmentio/encoding, github.com/segmentio/asm", License: "MIT",
		Copyright: "Copyright (c) 2019 Segment.io, Inc.",
		URL:       "https://github.com/segmentio/encoding", Kind: "go",
	},
	{
		Name: "github.com/yosida95/uritemplate/v3", License: "BSD-3-Clause",
		Copyright: "Copyright (c) 2015 Kohei YOSHIDA",
		URL:       "https://github.com/yosida95/uritemplate", Kind: "go",
	},
	{
		Name: "github.com/bluenviron/mediacommon/v2", License: "MIT",
		Copyright: "Copyright (c) 2023 bluenviron",
		URL:       "https://github.com/bluenviron/mediacommon", Kind: "go",
	},
	{
		Name: "github.com/pion/sdp/v3, pion/srtp/v3, pion/transport/v4, pion/logging, pion/randutil", License: "MIT",
		Copyright: "Copyright (c) 2023 The Pion community",
		URL:       "https://github.com/pion", Kind: "go",
	},
	{
		Name: "golang.org/x/net, golang.org/x/sys, golang.org/x/sync, golang.org/x/time, golang.org/x/oauth2", License: "BSD-3-Clause",
		Copyright: "Copyright 2009 The Go Authors",
		URL:       "https://go.googlesource.com/", Kind: "go",
	},
	{
		Name: "go.yaml.in/yaml/v3", Version: "v3.0.5", License: "MIT / Apache-2.0",
		Copyright: "Copyright (c) 2006-2011 Kirill Simonov, 2011-2019 Canonical Ltd",
		URL:       "https://github.com/yaml/go-yaml", Kind: "go",
	},
	{
		Name: "Go standard library", License: "BSD-3-Clause",
		Copyright: "Copyright 2009 The Go Authors",
		URL:       "https://go.dev/LICENSE", Kind: "go",
	},

	// --- Frontend (loaded from CDN by the embedded dashboard) ---
	{
		Name: "Tailwind CSS (Play CDN)", License: "MIT",
		Copyright: "Copyright (c) Tailwind Labs, Inc.",
		URL:       "https://github.com/tailwindlabs/tailwindcss", Kind: "frontend",
	},
	{
		Name: "Alpine.js", Version: "3.x", License: "MIT",
		Copyright: "Copyright (c) 2019-2021 Caleb Porzio and contributors",
		URL:       "https://github.com/alpinejs/alpine", Kind: "frontend",
	},
	{
		Name: "Scalar API Reference", License: "MIT",
		Copyright: "Copyright (c) 2023-2026 Scalar",
		URL:       "https://github.com/scalar/scalar", Kind: "frontend",
		Notes: "Loaded from jsDelivr by the /api/docs page to render the OpenAPI document.",
	},

	// --- Runtime tools invoked as separate processes (Docker image) ---
	{
		Name: "FFmpeg", License: "GPL-2.0-or-later / LGPL-2.1-or-later",
		Copyright: "Copyright (c) 2000-2025 the FFmpeg developers",
		URL:       "https://ffmpeg.org/", Kind: "runtime",
		Notes: "Invoked as an external process (not linked). The Alpine package is built with GPL components such as libx264/libx265.",
	},
	{
		Name: "espeak-ng", License: "GPL-3.0-or-later",
		Copyright: "Copyright (c) 1995-2014 Jonathan Duddington, 2015-2024 eSpeak NG contributors",
		URL:       "https://github.com/espeak-ng/espeak-ng", Kind: "tts",
		Notes: "Invoked as an external process for the English speaking clock.",
	},
	{
		Name: "Open JTalk", Version: "1.11", License: "Modified BSD",
		Copyright: "Copyright (c) 2008-2016 Nagoya Institute of Technology",
		URL:       "https://open-jtalk.sourceforge.net/", Kind: "tts",
		Notes: "Invoked as an external process for the Japanese speaking clock.",
	},
	{
		Name: "HTS Engine API", Version: "1.10", License: "Modified BSD",
		Copyright: "Copyright (c) 2001-2015 Nagoya Institute of Technology, 2001-2008 Tokyo Institute of Technology",
		URL:       "https://hts-engine.sourceforge.net/", Kind: "tts",
	},
	{
		Name: "NAIST Japanese Dictionary for Open JTalk (open_jtalk_dic_utf_8-1.11)", License: "BSD-3-Clause",
		Copyright: "Copyright (c) 2009 Nara Institute of Science and Technology",
		URL:       "https://open-jtalk.sourceforge.net/", Kind: "tts",
	},
	{
		Name: "HTS Voice tohoku-f01 (neutral)", License: "CC BY 4.0",
		Copyright: "Tohoku University, Graduate School of Information Sciences",
		URL:       "https://github.com/icn-lab/htsvoice-tohoku-f01", Kind: "tts",
		Notes: "This product uses the HTS voice model \"tohoku-f01-neutral\" created by the Tohoku University, Graduate School of Information Sciences, licensed under the Creative Commons Attribution 4.0 International License.",
	},
	{
		Name: "DejaVu Fonts", License: "Bitstream Vera License / public domain",
		Copyright: "Copyright (c) 2003 Bitstream, Inc.; DejaVu changes are in public domain",
		URL:       "https://dejavu-fonts.github.io/", Kind: "font",
		Notes: "Used by FFmpeg's drawtext filter for the OSD overlay.",
	},
}
