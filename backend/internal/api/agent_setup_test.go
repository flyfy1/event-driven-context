package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const setupProjectA = "prj_l4wcypqzs2be2pmyza3injqyw7"
const setupProjectB = "prj_aaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestAgentSetupMarkdownPublicRoute(t *testing.T) {
	f := newV2APIFixture(t)
	for locale, title := range map[string]string{
		"en": "Target project", "zh-CN": "目标项目", "ms": "Projek sasaran", "hi": "लक्ष्य प्रोजेक्ट",
	} {
		t.Run(locale, func(t *testing.T) {
			response := f.request(t, http.MethodGet, "/agent-setup.md?project="+setupProjectA+"&locale="+locale, "", "", nil)
			if response.Code != http.StatusOK {
				t.Fatalf("%d: %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, want := range []string{title, "`" + setupProjectA + "`", v2TestIssuer, "https://app.example/skills/edc-recorder/SKILL.md", "edc --server " + v2TestIssuer + " whoami", "query --project " + setupProjectA + " --limit 5", "state list --project " + setupProjectA} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, forbidden := range []string{"codex mcp", "claude mcp", "mcpServers", v2TestIssuer + "/mcp"} {
				if strings.Contains(body, forbidden) {
					t.Errorf("local setup must not contain %q", forbidden)
				}
			}
			if strings.Contains(body, "{{") || strings.Contains(body, "PROJECT_ID") || strings.Contains(body, f.project.Name) || strings.Contains(body, f.token) {
				t.Fatal("unrendered template or private data")
			}
			if got := response.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
				t.Fatal(got)
			}
			if got := response.Header().Get("Content-Language"); got != locale {
				t.Fatal(got)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatal(got)
			}
		})
	}
}

func TestAgentSetupParametersAndIsolation(t *testing.T) {
	handler := agentSetupHandler(Config{PublicBaseURL: "https://api.example", IntegAuth: IntegAuthConfig{WebBaseURL: "https://web.example"}})
	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://untrusted.example/agent-setup.md"+query, nil)
		r.Header.Set("X-Forwarded-Host", "attacker.example")
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	for _, project := range []string{setupProjectA, setupProjectB} {
		w := get("?project=" + project)
		if w.Code != 200 || !strings.Contains(w.Body.String(), project) || strings.Contains(w.Body.String(), "untrusted.example") || strings.Contains(w.Body.String(), "attacker.example") {
			t.Fatal(w.Body.String())
		}
		other := setupProjectA
		if project == other {
			other = setupProjectB
		}
		if strings.Contains(w.Body.String(), other) {
			t.Fatal("cross-request project leak")
		}
	}
	for _, project := range []string{"", "bad", "prj_a\n# instructions", "prj_a`", "../secret", "prj_" + strings.Repeat("a", 65)} {
		if w := get("?project=" + url.QueryEscape(project)); w.Code != 400 {
			t.Fatalf("accepted %q: %d", project, w.Code)
		}
	}
	for _, query := range []string{"?project=" + setupProjectA + "&project=" + setupProjectB, "?locale=en&locale=zh-CN", "?project=%ZZ", "?extra=" + strings.Repeat("a", 4096)} {
		if w := get(query); w.Code != 400 {
			t.Fatalf("accepted ambiguous/invalid query: %d", w.Code)
		}
	}
	w := get("")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Stop and ask") || !strings.Contains(w.Body.String(), "PROJECT_ID") {
		t.Fatal(w.Body.String())
	}
	for requested, expected := range map[string]string{"zh-SG": "zh-CN", "ms-MY": "ms", "hi-IN": "hi", "fr": "en"} {
		if w := get("?locale=" + requested); w.Header().Get("Content-Language") != expected {
			t.Fatal(requested, w.Header())
		}
	}
}

func TestAgentSetupRequiresTrustedURLs(t *testing.T) {
	for _, raw := range []string{"", "https://api.example/evil\n", "https://api.example/$(cmd)", "https://user:pass@api.example", "https://api.example?token=secret", "javascript:alert(1)", "http://api.example"} {
		if _, ok := agentSetupBase(raw); ok {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://api.example", "https://api.example/", "http://127.0.0.1:8401", "http://localhost:8401"} {
		if _, ok := agentSetupBase(raw); !ok {
			t.Fatalf("rejected %q", raw)
		}
	}
	w := httptest.NewRecorder()
	agentSetupHandler(Config{})(w, httptest.NewRequest("GET", "http://spoofed.example/agent-setup.md", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
