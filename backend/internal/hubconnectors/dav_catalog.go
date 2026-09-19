package hubconnectors

// Protocols: https://www.rfc-editor.org/rfc/rfc4791.html (CalDAV)
// https://www.rfc-editor.org/rfc/rfc6352.html (CardDAV).
// iCloud app-specific passwords: https://support.apple.com/en-us/102654.
func davProviders() []Provider {
	provider := func(id, name string, calendar, icloud bool) Provider {
		operation := Operation{ID: "contacts.list", Description: "Read bounded name/email/phone metadata from the configured address book", ReadOnly: true, InputSchema: schema(map[string]any{"query": textParam(256), "limit": limitParam()})}
		if calendar {
			operation = Operation{ID: "calendars.query", Description: "Query VEVENT resources over whole-second RFC3339 timestamps at most 7 days apart; original ICS is not recurrence-expanded", ReadOnly: true, InputSchema: schema(map[string]any{"time_min": textParam(64), "time_max": textParam(64), "limit": limitParam()}, "time_min", "time_max")}
		}
		requirements := []string{"Owner-supplied JSON credential with url, username and password for one exact collection", "Collection URL must be public HTTPS on port 443, end in '/', and contain no query, URL credentials or fragment", "Server must support read-only CalDAV/CardDAV REPORT on that collection"}
		if icloud {
			requirements = append(requirements, "Exact iCloud collection URL on an icloud.com host and an owner-created Apple app-specific password; never supply the primary Apple password")
		}
		return Provider{ID: id, Name: name, AuthMode: "owner_endpoint_password", ImplementationStatus: "adapter_available", MultipleAccounts: true, Operations: []Operation{operation}, Requirements: requirements, Limitations: []string{"One selected collection per connection; Agent grant applies to the connection and operation, not an independently enforced per-event policy", "No discovery, redirects, following resource hrefs, writes, account login, automatic refresh, or background sync", "Private-network and loopback self-hosted servers are not enabled; use a public HTTPS endpoint", "At most 100 records and 2 MiB per response; partial or limited results are explicitly marked incomplete and have no continuation cursor", "Calendar filtering is performed by the server; original recurrence masters may describe dates outside the requested interval", "Contact output omits photos, keys, notes, arbitrary properties and server executable URLs"}}
	}
	return []Provider{provider("caldav", "CalDAV Calendar", true, false), provider("carddav", "CardDAV Contacts", false, false), provider("icloud-calendar", "iCloud Calendar", true, true), provider("icloud-contacts", "iCloud Contacts", false, true)}
}
