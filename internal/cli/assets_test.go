package cli

import (
	"strings"
	"testing"
)

// TestRenderTemplates renders the workflow assets the CLI prints/scaffolds and checks the
// placeholders are filled, the values land, and the literal GitHub ${{ }} expressions survive
// (render does plain {{.Key}} replacement, so it must not touch ${{ ... }}).
func TestRenderTemplates(t *testing.T) {
	data := map[string]string{
		"Target":    "statio.example.ts.net",
		"Service":   "api",
		"Image":     "ghcr.io/org/api",
		"ActionRef": "accentiostudios/statio@v1",
	}
	for _, name := range []string{"deploy.yml.tmpl", "statio-step.snippet.tmpl"} {
		out, err := render(name, data)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		s := string(out)
		if strings.Contains(s, "{{.") {
			t.Errorf("%s: leftover placeholder in output:\n%s", name, s)
		}
		for _, want := range []string{"ghcr.io/org/api", "accentiostudios/statio@v1", "statio.example.ts.net"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
		if !strings.Contains(s, "${{ secrets.STATIO_TS_OAUTH_CLIENT_ID }}") {
			t.Errorf("%s: lost the OAuth secret expression (render ate a ${{ }})", name)
		}
	}
}
