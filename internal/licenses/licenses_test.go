package licenses

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryDirectGoModuleIsAttributed parses go.mod and verifies each direct
// requirement appears in the component list, so a new dependency cannot be
// added without updating the attribution shown in the UI.
func TestEveryDirectGoModuleIsAttributed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Skipf("go.mod not found: %v", err)
	}

	inBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "require (":
			inBlock = true
			continue
		case line == ")":
			inBlock = false
			continue
		case !inBlock || line == "" || strings.HasSuffix(line, "// indirect"):
			continue
		}
		fields := strings.Fields(line)
		module, version := fields[0], fields[1]

		var found *Component
		for i := range Components {
			if strings.Contains(Components[i].Name, module) {
				found = &Components[i]
				break
			}
		}
		if found == nil {
			t.Errorf("direct dependency %s is missing from licenses.Components", module)
			continue
		}
		if found.Version != "" && found.Version != version {
			t.Errorf("%s: attributed version %s differs from go.mod %s", module, found.Version, version)
		}
	}
}

func TestComponentsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Components {
		if c.Name == "" || c.License == "" || c.URL == "" || c.Kind == "" {
			t.Errorf("component %+v is missing a required field", c)
		}
		if seen[c.Name] {
			t.Errorf("duplicate component %q", c.Name)
		}
		seen[c.Name] = true
		switch c.Kind {
		case "go", "frontend", "runtime", "tts", "font":
		default:
			t.Errorf("component %q has unknown kind %q", c.Name, c.Kind)
		}
	}

	// CC BY assets must carry attribution wording.
	for _, c := range Components {
		if strings.HasPrefix(c.License, "CC BY") && c.Notes == "" {
			t.Errorf("%s is CC BY licensed but has no attribution note", c.Name)
		}
	}

	// The frontend CDN libraries referenced by index.html must be listed.
	html, err := os.ReadFile(filepath.Join("..", "web", "static", "index.html"))
	if err != nil {
		t.Skipf("index.html not found: %v", err)
	}
	for cdn, name := range map[string]string{"cdn.tailwindcss.com": "tailwind", "alpinejs": "alpine"} {
		if !strings.Contains(string(html), cdn) {
			continue
		}
		ok := false
		for _, c := range Components {
			if c.Kind == "frontend" && strings.Contains(strings.ToLower(c.Name), name) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("index.html loads %s but it is not attributed", cdn)
		}
	}
}
