package api

import (
	"bytes"
	"embed"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"text/template"
)

//go:embed agent_setup/*.md
var agentSetupFiles embed.FS

var agentSetupTemplates = template.Must(template.ParseFS(agentSetupFiles, "agent_setup/*.md"))
var agentSetupProjectID = regexp.MustCompile(`^prj_[a-z0-9]{1,64}$`)

// This public resource renders only caller-provided routing data and static
// instructions. It never looks up a project, its name, members or credentials.
func agentSetupHandler(config Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(r.URL.RawQuery) > 4096 || len(query["project"]) > 1 || len(query["locale"]) > 1 {
			http.Error(w, "invalid setup parameters", http.StatusBadRequest)
			return
		}
		project := query.Get("project")
		if (query.Has("project") && project == "") || (project != "" && !agentSetupProjectID.MatchString(project)) {
			http.Error(w, "invalid project ID", http.StatusBadRequest)
			return
		}
		locale := normalizeOAuthLanguage(query.Get("locale"))
		if locale == "" {
			locale = "en"
		}
		// Use deployment configuration, never Host or X-Forwarded-Host, to build
		// URLs that agents may execute as commands or use for authentication.
		apiBase, ok := agentSetupBase(config.PublicBaseURL)
		if !ok {
			http.Error(w, "setup guide requires a configured public API URL", http.StatusServiceUnavailable)
			return
		}
		webBase, ok := agentSetupBase(config.IntegAuth.WebBaseURL)
		if !ok && config.IntegAuth.WebBaseURL == "" && len(config.AllowedOrigins) > 0 {
			webBase, ok = agentSetupBase(config.AllowedOrigins[0])
		}
		if !ok {
			http.Error(w, "setup guide requires a configured web URL", http.StatusServiceUnavailable)
			return
		}
		data := struct {
			Project, APIURL, SkillURL, RecallSkillURL string
			HasProject                                bool
		}{
			Project: project, HasProject: project != "",
			APIURL: apiBase, SkillURL: webBase + "/skills/edc-recorder/SKILL.md",
			RecallSkillURL: webBase + "/skills/memory-recall/SKILL.md",
		}
		if !data.HasProject {
			data.Project = "PROJECT_ID"
		}
		var body bytes.Buffer
		if err := agentSetupTemplates.ExecuteTemplate(&body, locale+".md", data); err != nil {
			http.Error(w, "setup guide unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Language", locale)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body.Bytes())
	}
}

func agentSetupBase(raw string) (string, bool) {
	if strings.ContainsAny(raw, "\r\n\t `\"'\\$<>|;(){}") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return "", false
	}
	return strings.TrimRight(u.String(), "/"), true
}
