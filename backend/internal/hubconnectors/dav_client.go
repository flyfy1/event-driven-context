package hubconnectors

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const maxDAVBytes = 2 * 1024 * 1024
const davNamespace = "DAV:"
const calDAVNamespace = "urn:ietf:params:xml:ns:caldav"
const cardDAVNamespace = "urn:ietf:params:xml:ns:carddav"

type davCredential struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *Client) executeDAV(ctx context.Context, providerID, operationID, credential string, args map[string]any) (Result, error) {
	var operation *Operation
	for _, p := range davProviders() {
		if p.ID == providerID {
			for i := range p.Operations {
				if p.Operations[i].ID == operationID {
					operation = &p.Operations[i]
				}
			}
		}
	}
	if operation == nil {
		return Result{}, &Error{Code: "operation_unavailable"}
	}
	if err := validateArgs(operation.InputSchema, args); err != nil {
		return Result{}, err
	}
	config, collection, err := parseDAVCredential(providerID, credential)
	if err != nil {
		return Result{}, err
	}
	calendar := operationID == "calendars.query"
	limit, _ := strconv.Atoi(integerArg(args, "limit", 20))
	body, window, err := davReport(calendar, args, limit)
	if err != nil {
		return Result{}, err
	}
	transport := http.RoundTripper(feedTransport())
	if c.http.Transport != nil {
		transport = c.http.Transport
	}
	client := http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "REPORT", collection.String(), strings.NewReader(body))
	if err != nil {
		return Result{}, &Error{Code: "invalid_dav_endpoint"}
	}
	req.SetBasicAuth(config.Username, config.Password)
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml, text/xml")
	res, err := client.Do(req)
	if err != nil {
		return Result{}, &Error{Code: "dav_upstream_unavailable"}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		code := "dav_upstream_failed"
		switch res.StatusCode {
		case 401:
			code = "reauthorization_required"
		case 403:
			code = "provider_permission_denied"
		case 429:
			code = "provider_rate_limited"
		case 507:
			code = "provider_query_too_large"
		}
		return Result{}, &Error{Code: code, StatusCode: res.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxDAVBytes+1))
	if err != nil {
		return Result{}, &Error{Code: "dav_upstream_read_failed"}
	}
	if len(data) > maxDAVBytes {
		return Result{}, &Error{Code: "dav_response_too_large"}
	}
	return parseDAVResponse(data, collection, calendar, limit, window)
}

func parseDAVCredential(providerID, credential string) (davCredential, *url.URL, error) {
	config := davCredential{}
	invalid := &Error{Code: "invalid_dav_credential"}
	if len(credential) == 0 || len(credential) > 16384 {
		return config, nil, invalid
	}
	d := json.NewDecoder(strings.NewReader(credential))
	d.DisallowUnknownFields()
	if d.Decode(&config) != nil {
		return config, nil, invalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return config, nil, invalid
	}
	if config.Username == "" || len(config.Username) > 512 || strings.ContainsAny(config.Username, ":\x00\r\n") || config.Password == "" || len(config.Password) > 4096 || strings.ContainsAny(config.Password, "\x00\r\n") {
		return config, nil, invalid
	}
	u, err := feedURL(config.URL)
	if err != nil {
		return config, nil, &Error{Code: "invalid_dav_endpoint"}
	}
	if u.RawQuery != "" || u.ForceQuery || !strings.HasSuffix(u.Path, "/") || u.Path == "/" || strings.ContainsAny(u.Path, "\\\x00\r\n") || path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") {
		return config, nil, &Error{Code: "invalid_dav_endpoint"}
	}
	if strings.HasPrefix(providerID, "icloud-") {
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if host != "icloud.com" && !strings.HasSuffix(host, ".icloud.com") {
			return config, nil, &Error{Code: "invalid_icloud_endpoint"}
		}
	}
	return config, u, nil
}

func davEscape(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

func davReport(calendar bool, args map[string]any, limit int) (string, map[string]string, error) {
	if calendar {
		min, _ := args["time_min"].(string)
		max, _ := args["time_max"].(string)
		start, e1 := time.Parse(time.RFC3339, min)
		end, e2 := time.Parse(time.RFC3339, max)
		if e1 != nil || e2 != nil || start.Nanosecond() != 0 || end.Nanosecond() != 0 || !end.After(start) || end.Sub(start) > 7*24*time.Hour {
			return "", nil, &Error{Code: "invalid_time_range"}
		}
		start = start.UTC()
		end = end.UTC()
		// No expand element: return original components including recurrence rules.
		report := `<?xml version="1.0" encoding="utf-8"?><C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/><C:calendar-data/></D:prop><C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"><C:time-range start="` + start.Format("20060102T150405Z") + `" end="` + end.Format("20060102T150405Z") + `"/></C:comp-filter></C:comp-filter></C:filter></C:calendar-query>`
		return report, map[string]string{"time_min": start.Format(time.RFC3339), "time_max": end.Format(time.RFC3339)}, nil
	}
	query, _ := args["query"].(string)
	filter := `<C:filter><C:prop-filter name="FN"/></C:filter>`
	if query != "" {
		escaped := davEscape(query)
		filter = `<C:filter test="anyof"><C:prop-filter name="FN"><C:text-match collation="i;unicode-casemap" match-type="contains">` + escaped + `</C:text-match></C:prop-filter><C:prop-filter name="EMAIL"><C:text-match collation="i;unicode-casemap" match-type="contains">` + escaped + `</C:text-match></C:prop-filter></C:filter>`
	}
	return `<?xml version="1.0" encoding="utf-8"?><C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:prop><D:getetag/><C:address-data><C:prop name="VERSION"/><C:prop name="UID"/><C:prop name="FN"/><C:prop name="N"/><C:prop name="EMAIL"/><C:prop name="TEL"/><C:prop name="ORG"/><C:prop name="TITLE"/></C:address-data></D:prop>` + filter + fmt.Sprintf(`<C:limit><C:nresults>%d</C:nresults></C:limit></C:addressbook-query>`, limit+1), nil, nil
}
