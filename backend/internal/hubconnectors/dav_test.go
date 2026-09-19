package hubconnectors

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func davTestCredential(endpoint string) string {
	b, _ := json.Marshal(davCredential{URL: endpoint, Username: "owner@example.invalid", Password: "app-specific-secret"})
	return string(b)
}
func davTestBody(calendar bool, href, data string) string {
	ns, tag := cardDAVNamespace, "address-data"
	if calendar {
		ns, tag = calDAVNamespace, "calendar-data"
	}
	return `<D:multistatus xmlns:D="DAV:" xmlns:C="` + ns + `"><D:response><D:href>` + davEscape(href) + `</D:href><D:propstat><D:prop><D:getetag>etag-1</D:getetag><C:` + tag + `>` + davEscape(data) + `</C:` + tag + `></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
}
func davTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: 207, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestDAVCalendarReportAndOriginalRecurrence(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:event1\r\nDTSTART:20260919T000000Z\r\nRRULE:FREQ=DAILY;COUNT=30\r\nSUMMARY:Planning\r\nEND:VEVENT\r\nEND:VCALENDAR"
	for _, provider := range []string{"caldav", "icloud-calendar"} {
		t.Run(provider, func(t *testing.T) {
			endpoint := "https://calendar.example.invalid/work/"
			if provider == "icloud-calendar" {
				endpoint = "https://p01-caldav.icloud.com/owner/work/"
			}
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "REPORT" || r.URL.String() != endpoint || r.Header.Get("Depth") != "1" {
					t.Fatal("wrong DAV request")
				}
				username, password, ok := r.BasicAuth()
				if !ok || username != "owner@example.invalid" || password != "app-specific-secret" {
					t.Fatal("missing account authentication")
				}
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `start="20260918T160000Z" end="20260920T160000Z"`) || strings.Contains(string(body), "expand") {
					t.Fatal("wrong UTC interval or fabricated expansion")
				}
				var root struct{ XMLName xml.Name }
				if xml.Unmarshal(body, &root) != nil || root.XMLName.Local != "calendar-query" || root.XMLName.Space != calDAVNamespace {
					t.Fatal("invalid calendar REPORT XML")
				}
				return davTestResponse(davTestBody(true, endpoint+"one.ics", ics)), nil
			})})
			result, err := client.executeDAV(context.Background(), provider, "calendars.query", davTestCredential(endpoint), map[string]any{"time_min": "2026-09-19T00:00:00+08:00", "time_max": "2026-09-21T00:00:00+08:00"})
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Items              []davItem `json:"items"`
				Incomplete         bool      `json:"incomplete"`
				RecurrenceExpanded bool      `json:"recurrence_expanded"`
				Filtering          string    `json:"filtering"`
			}
			if json.Unmarshal(result.Body, &out) != nil || len(out.Items) != 1 || out.Incomplete || out.RecurrenceExpanded || out.Filtering != "server_time_range" {
				t.Fatal("invalid calendar output", string(result.Body))
			}
			if !strings.Contains(out.Items[0].ICS, "RRULE:FREQ=DAILY;COUNT=30") || strings.HasPrefix(out.Items[0].Href, "https:") {
				t.Fatal("recurrence lost or executable href returned")
			}
			if calls != 1 {
				t.Fatal("followed server href")
			}
		})
	}
}

func TestDAVCardReportEscapingAndProjection(t *testing.T) {
	card := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:contact1\r\nFN:Alice & Bob\r\nN:Smith;Alice;;;\r\nEMAIL;TYPE=WORK:alice@example.invalid\r\nTEL:+65 12345678\r\nORG:Example\r\nTITLE:Engi\r\n neer\r\nPHOTO;ENCODING=b:SECRET_BINARY\r\nKEY:SECRET_KEY\r\nNOTE:Private notes excluded\r\nURL:https://attacker.invalid\r\nEND:VCARD"
	for _, provider := range []string{"carddav", "icloud-contacts"} {
		t.Run(provider, func(t *testing.T) {
			endpoint := "https://contacts.example.invalid/people/"
			if provider == "icloud-contacts" {
				endpoint = "https://p01-contacts.icloud.com/owner/people/"
			}
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != "REPORT" || r.URL.String() != endpoint || !strings.Contains(string(body), "Alice &amp; &lt;Bob&gt;") || !strings.Contains(string(body), "<C:nresults>4</C:nresults>") {
					t.Fatal("unescaped or unbounded CardDAV query")
				}
				var root struct{ XMLName xml.Name }
				if xml.Unmarshal(body, &root) != nil || root.XMLName.Local != "addressbook-query" {
					t.Fatal("invalid CardDAV XML")
				}
				return davTestResponse(davTestBody(false, endpoint+"one.vcf", card)), nil
			})})
			result, err := client.executeDAV(context.Background(), provider, "contacts.list", davTestCredential(endpoint), map[string]any{"query": "Alice & <Bob>", "limit": 3})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(result.Body), "SECRET_") || strings.Contains(string(result.Body), "Private notes") || strings.Contains(string(result.Body), "attacker.invalid") {
				t.Fatal("unrequested contact fields leaked")
			}
			var out struct {
				Items      []davItem `json:"items"`
				Incomplete bool      `json:"incomplete"`
			}
			if json.Unmarshal(result.Body, &out) != nil || len(out.Items) != 1 || out.Incomplete || out.Items[0].Contact.Name != "Alice & Bob" || out.Items[0].Contact.Title != "Engineer" {
				t.Fatal("lost contact metadata", string(result.Body))
			}
		})
	}
}

func TestDAVRejectsUnsafeConfigurationAndInput(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("network call with unsafe config")
		return nil, nil
	})})
	for _, endpoint := range []string{"http://dav.example.com/collection/", "https://127.0.0.1/collection/", "https://[::1]/collection/", "https://169.254.169.254/collection/", "https://10.0.0.2/collection/", "https://server.local/collection/", "https://dav.example.com:8443/collection/", "https://user:password@dav.example.com/collection/", "https://dav.example.com/collection/?token=secret", "https://dav.example.com/collection/#fragment", "https://dav.example.com/collection", "https://dav.example.com/", "https://dav.example.com/collection/../other/"} {
		if _, err := client.executeDAV(context.Background(), "carddav", "contacts.list", davTestCredential(endpoint), nil); err == nil {
			t.Errorf("unsafe endpoint accepted %s", endpoint)
		}
	}
	if _, err := client.executeDAV(context.Background(), "icloud-contacts", "contacts.list", davTestCredential("https://icloud.com.evil.example/contacts/"), nil); err == nil {
		t.Fatal("untrusted iCloud endpoint accepted")
	}
	credential := davTestCredential("https://dav.example.com/collection/")
	for _, args := range []map[string]any{
		{"time_min": "2026-09-19T00:00:00Z", "time_max": "2026-09-27T00:00:00Z"},
		{"time_min": "2026-09-19T00:00:00Z", "time_max": "2026-09-19T00:00:00Z"},
		{"time_min": "not-a-date", "time_max": "2026-09-20T00:00:00Z"},
		{"time_min": "2026-09-19T00:00:00.1Z", "time_max": "2026-09-20T00:00:00Z"},
		{"time_min": "2026-09-19T00:00:00Z", "time_max": "2026-09-20T00:00:00Z", "url": "https://evil.example/"},
	} {
		if _, err := client.executeDAV(context.Background(), "caldav", "calendars.query", credential, args); err == nil {
			t.Fatal("invalid time/query accepted")
		}
	}
	if _, err := client.executeDAV(context.Background(), "carddav", "contacts.list", credential, map[string]any{"limit": 101}); err == nil {
		t.Fatal("unbounded limit accepted")
	}
	for _, invalid := range []string{credential + `{}`, `{"url":"https://dav.example.com/c/","username":"a:b","password":"secret"}`, `{"url":"https://dav.example.com/c/","username":"a","password":"secret","headers":{}}`} {
		if _, err := client.executeDAV(context.Background(), "carddav", "contacts.list", invalid, nil); err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}

func TestDAVXMLBoundsNamespacesAndIncomplete(t *testing.T) {
	collection, _ := url.Parse("https://dav.example.com/contacts/")
	card := "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD"
	valid := davTestBody(false, "/contacts/a.vcf", card)
	for _, invalid := range []string{
		`<!DOCTYPE x [<!ENTITY secret SYSTEM "file:///etc/passwd">]>` + valid,
		`<!DOCTYPE x [<!ENTITY secret "expanded">]>` + valid,
		strings.Replace(valid, "Alice", "&undefined;", 1),
		strings.ReplaceAll(valid, `xmlns:D="DAV:"`, `xmlns:D="evil"`),
		valid + valid,
		`<D:multistatus xmlns:D="DAV:">` + strings.Repeat("<x>", 33) + strings.Repeat("</x>", 33) + `</D:multistatus>`,
	} {
		if _, err := parseDAVResponse([]byte(invalid), collection, false, 20, nil); err == nil {
			t.Fatal("unsafe XML accepted")
		}
	}
	// Over-limit records are reported, never silently claimed complete.
	start := strings.Index(valid, "<D:response>")
	end := strings.Index(valid, "</D:response>") + len("</D:response>")
	duplicate := valid[:end] + valid[start:end] + valid[end:]
	result, err := parseDAVResponse([]byte(duplicate), collection, false, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items      []davItem `json:"items"`
		Incomplete bool      `json:"incomplete"`
	}
	if json.Unmarshal(result.Body, &out) != nil || len(out.Items) != 1 || !out.Incomplete {
		t.Fatal("missing partial marker")
	}
	for _, href := range []string{"https://attacker.example/contacts/a.vcf", "/other/a.vcf", "../outside.vcf", "/contacts/a.vcf?secret=token", "//attacker.example/contacts/a.vcf"} {
		result, err := parseDAVResponse([]byte(davTestBody(false, href, card)), collection, false, 20, nil)
		if err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(result.Body, &out) != nil || len(out.Items) != 0 || !out.Incomplete {
			t.Fatal("unsafe resource href exposed")
		}
	}
	partial := strings.Replace(valid, "HTTP/1.1 200 OK", "HTTP/1.1 403 Forbidden", 1)
	result, err = parseDAVResponse([]byte(partial), collection, false, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(result.Body, &out) != nil || !out.Incomplete || len(out.Items) != 0 {
		t.Fatal("failed propstat presented as success")
	}
}

func TestDAVUpstreamFailuresAreBoundedAndRedacted(t *testing.T) {
	for _, scenario := range []string{"unauthorized", "redirect", "oversized", "transport"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				res := davTestResponse("app-specific-secret private-user-data")
				switch scenario {
				case "unauthorized":
					res.StatusCode = 401
				case "redirect":
					res.StatusCode = 302
					res.Header.Set("Location", "https://other.example/collection/")
				case "oversized":
					res.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxDAVBytes+1)))
				case "transport":
					return nil, errors.New("app-specific-secret " + r.URL.String())
				}
				return res, nil
			})})
			_, err := client.executeDAV(context.Background(), "carddav", "contacts.list", davTestCredential("https://dav.example.com/contacts/"), nil)
			if err == nil {
				t.Fatal("expected failure")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "http") || strings.Contains(err.Error(), "private-user") {
				t.Fatal("credential or body leaked")
			}
			if calls != 1 {
				t.Fatal("redirect followed or retry occurred")
			}
		})
	}
}
