package hubconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

type feedTestTransport func(*http.Request) (*http.Response, error)

func (f feedTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFeedPublicEndpointValidation(t *testing.T) {
	for _, raw := range []string{"http://public.example/feed", "https://localhost/feed", "https://secret.local/feed", "https://metadata.google.internal/feed", "https://user:secret@public.example/feed", "https://public.example:8443/feed", "https://127.0.0.1/feed", "https://169.254.169.254/feed", "https://10.0.0.1/feed", "https://[::1]/feed", "https://[::ffff:127.0.0.1]/feed"} {
		if _, err := feedURL(raw); err == nil {
			t.Errorf("accepted unsafe endpoint %s", raw)
		}
	}
	for _, ip := range []string{"0.0.0.0", "100.64.0.1", "172.16.0.1", "192.168.1.1", "198.18.0.1", "224.1.1.1", "::", "fc00::1", "fe80::1", "64:ff9b::a00:1", "2002:0a00:0001::1"} {
		if feedPublicIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted special IP %s", ip)
		}
	}
	for _, raw := range []string{"https://feeds.example/feed.xml", "https://8.8.8.8:443/feed"} {
		if _, err := feedURL(raw); err != nil {
			t.Fatal("public endpoint rejected", err)
		}
	}
	if !feedPublicIP(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Fatal("public IPv6 rejected")
	}
}
func TestFeedDNSPinsValidatedPublicIP(t *testing.T) {
	lookups, dials := 0, 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	marker := errors.New("test stopped before dialing")
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		dials++
		if network != "tcp" || address != "8.8.8.8:443" {
			t.Fatal("dial did not pin verified IP", address)
		}
		return nil, marker
	}
	if _, err := dialPublicFeed(context.Background(), "feed.example:443", lookup, dial); !errors.Is(err, marker) {
		t.Fatal(err)
	}
	if lookups != 1 || dials != 1 {
		t.Fatal("resolver repeated before connect")
	}
	lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	if _, err := dialPublicFeed(context.Background(), "feed.example:443", lookup, dial); err == nil {
		t.Fatal("mixed public/private DNS accepted")
	}
	if dials != 1 {
		t.Fatal("dialed private DNS response")
	}
	transport := feedTransport()
	if transport.Proxy != nil {
		t.Fatal("feed honors proxy")
	}
}
func TestFeedRSSAndAtomOutput(t *testing.T) {
	fixtures := []string{
		`<rss version="2.0"><channel><title>Example RSS</title><item><title>First</title><link>https://example.org/story</link><description><![CDATA[<p>Hello <b>world</b></p>]]></description><pubDate>today</pubDate></item><item><title>Second</title></item></channel></rss>`,
		`<feed xmlns="http://www.w3.org/2005/Atom"><title>Example Atom</title><entry><title>First</title><link rel="alternate" href="https://example.org/story"/><summary>Hello world</summary><updated>today</updated></entry><entry><title>Second</title></entry></feed>`,
	}
	for _, fixture := range fixtures {
		result, err := parseFeed([]byte(fixture), 1)
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			Entries []feedEntry `json:"entries"`
			Notice  string      `json:"notice"`
		}
		if err := json.Unmarshal(result.Body, &output); err != nil {
			t.Fatal(err)
		}
		if len(output.Entries) != 1 || output.Entries[0].Title != "First" || output.Entries[0].Text != "Hello world" || output.Entries[0].Date != "today" || output.Notice == "" {
			t.Fatalf("incorrect feed output: %s", result.Body)
		}
	}
	for _, fixture := range []string{`<!DOCTYPE rss [<!ENTITY x SYSTEM "file:///etc/passwd">]><rss/>`, `<rss/><rss/>`, `<html>not a feed</html>`, `<rss>` + strings.Repeat("<a>", 40) + strings.Repeat("</a>", 40) + `</rss>`} {
		if _, err := parseFeed([]byte(fixture), 10); err == nil {
			t.Fatal("unsafe/invalid XML accepted")
		}
	}
	if feedLink("javascript:alert(1)") != "" || feedLink("https://user:pass@example.org/") != "" {
		t.Fatal("unsafe link accepted")
	}
}
func TestFeedBoundedFetchAndNoAgentURL(t *testing.T) {
	calls := 0
	status := 200
	body := `<rss><channel><title>Feed</title></channel></rss>`
	client := NewClient(&http.Client{Transport: feedTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://feed.example/rss" || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected endpoint or credential forwarded")
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://127.0.0.1/secret"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	credential := `{"url":"https://feed.example/rss"}`
	if _, err := client.Execute(context.Background(), "rss-feed", "entries.list", credential, nil); err != nil {
		t.Fatal(err)
	}
	status = 302
	if _, err := client.Execute(context.Background(), "rss-feed", "entries.list", credential, nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls != 2 {
		t.Fatal("followed redirect")
	}
	status = 200
	body = strings.Repeat("x", maxFeedBytes+1)
	if _, err := client.Execute(context.Background(), "rss-feed", "entries.list", credential, nil); err == nil {
		t.Fatal("oversize response accepted")
	}
	if _, err := client.Execute(context.Background(), "rss-feed", "entries.list", `{"url":"https://feed.example/rss","headers":{"Authorization":"secret"}}`, nil); err == nil {
		t.Fatal("custom credential options accepted")
	}
	operation := feedProviders()[0].Operations[0]
	if err := validateArgs(operation.InputSchema, map[string]any{"url": "https://another.example/feed"}); err == nil {
		t.Fatal("agent URL override accepted")
	}
}
