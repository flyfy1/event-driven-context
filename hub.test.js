const test = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const fs = require("node:fs");
const utils = require("./workspace-utils.js");

// A small DOM harness makes delayed session and approval races deterministic.
class Element {
  constructor(tag = "div") { this.tagName = tag; this.children = []; this.listeners = {}; this.dataset = {}; this.attributes = {}; this._text = ""; this.value = ""; this.classList = { toggle() {} }; }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map((c) => c.textContent).join(""); }
  set innerHTML(_) { throw new Error("Untrusted HTML must never be parsed"); }
  get childNodes() { return this.children; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this._text = ""; this.children = children; }
  addEventListener(type, listener) { this.listeners[type] = listener; }
  setAttribute(name, value) { this.attributes[name] = value; }
  focus() { this.focused = true; }
  click() { if (!this.disabled) this.listeners.click?.({ target: this }); }
}
function descendants(element) { return element.children.flatMap((child) => [child, ...descendants(child)]); }
const flush = async () => { for (let i = 0; i < 12; i++) await new Promise(setImmediate); };
function fixture() {
  const now = Date.now();
  return {
    agents: [{ id: "agent_a", name: "<img src=x onerror=alert(1)>", status: "pending", verification_code: "CLI-CODE", created_at: new Date(now).toISOString(), expires_at: new Date(now + 900000).toISOString() }],
    requests: [], connections: [{ id: "connection_a", provider_id: "gmail", account_id: "work@example.test", display_name: "Work mail", status: "configured" }],
  };
}
function integrationRow(id, connections = [], overrides = {}) {
  return { provider: { id, name: id, implementation_status: "adapter_available", operations: [{ id: "read", read_only: true }], limitations: ["Owner approval required"] }, deployment_configured: true, connectable: true, connected_account_count: connections.length, visible: connections.length > 0, hidden_reason: connections.length ? "" : "no_configured_accounts", onboarding_method: "browser_oauth", connections: connections.map((connection) => ({ ...connection, operations: [{ id: "read", read_only: true }] })), ...overrides };
}
function registryFixture(data, configured = true) {
  return { providers: [integrationRow("gmail", data.connections, configured ? {} : { deployment_configured: false, connectable: false, visible: false }), integrationRow("google-drive", [], configured ? {} : { deployment_configured: false, connectable: false })], feature_flags: { hub_approvals: true, google_oauth: configured, push: false } };
}
function harness(options = {}) {
  const map = new Map();
  for (const match of fs.readFileSync(__dirname + "/hub.html", "utf8").matchAll(/id="([^"]+)"/g)) map.set("#" + match[1], new Element());
  map.set(".hub-nav", new Element("nav"));
  const storage = new Map();
  const windowEvents = {};
  const docEvents = {};
  const calls = [];
  const navigations = [];
  let identity = { id: "owner_a", email: "owner@example.test" };
  let data = options.data || fixture();
  let registry = Object.hasOwn(options, "registry") ? options.registry : registryFixture(data, options.googleConfigured !== false);
  const document = { hidden: false, documentElement: {}, querySelector: (selector) => map.get(selector) || null,
    createElement: (tag) => new Element(tag),
    querySelectorAll: (selector) => selector === "#hub-content button" ? ["agents", "requests", "connections"].flatMap((key) => descendants(map.get("#hub-" + key)).filter((el) => el.tagName === "button")) : [],
    addEventListener: (type, listener) => { docEvents[type] = listener; } };
  const location = new URL("http://localhost:8080/hub.html");
  location.assign = (url) => navigations.push(url);
  map.get("#hub-google-provider").value = "google-drive";
  const context = {
    document, location, URL, URLSearchParams, Intl, Date, AbortController,
    navigator: { language: "en" }, history: { replaceState() {} },
    localStorage: { getItem: (key) => storage.get(key) || null, setItem: (key, value) => storage.set(key, value) },
    setTimeout: () => 1, clearTimeout() {},
    fetch: async (url, config) => {
      calls.push({ url, config });
      if (url.endsWith("/v1/me")) return { ok: true, status: 200, json: async () => identity };
      if (url.endsWith("/v1/hub/owner")) { if (options.owner) return options.owner(); return { ok: true, status: 200, json: async () => data }; }
      if (url.endsWith("/v1/hub/integrations")) { if (options.integrations) return options.integrations(); return { ok: true, status: 200, json: async () => registry }; }
      if (url.endsWith("/v1/hub/google/start")) {
        if (options.googleStart) return options.googleStart();
        return { ok: true, status: 200, json: async () => ({ authorization_url: options.authorizationURL || "https://accounts.google.com/o/oauth2/v2/auth?state=opaque" }) };
      }
      if (url.includes("/decision")) return { ok: true, status: 200, json: async () => ({ ok: true }) };
      throw new Error("Unexpected URL " + url);
    },
    ContextWorkspaceUtils: utils,
    ContextNavigation: { readSession: async () => identity },
    addEventListener: (type, listener) => { windowEvents[type] = listener; }, confirm: () => true,
  };
  context.window = context;
  vm.runInNewContext(fs.readFileSync(__dirname + "/hub.js", "utf8"), context);
  return { map, calls, navigations, storage, windowEvents, docEvents, document, setIdentity: (next) => { identity = next; }, setData: (next) => { data = next; }, setRegistry: (next) => { registry = next; } };
}

test("Hub renders untrusted names as text and approval requires the CLI code", async () => {
  const h = harness(); await flush();
  assert.match(h.map.get("#hub-agents").textContent, /<img src=x onerror=alert\(1\)>/);
  assert.doesNotMatch(h.map.get("#hub-agents").textContent, /CLI-CODE/);
  const elements = descendants(h.map.get("#hub-agents"));
  const input = elements.find((e) => e.tagName === "input");
  const approve = elements.find((e) => e.tagName === "button" && e.textContent === "Approve");
  input.value = "wrong"; approve.click(); await flush();
  assert.equal(h.calls.filter((c) => c.config.method === "POST").length, 0);
  assert.match(h.map.get("#hub-message").textContent, /does not match/);
  input.value = "cli-code"; approve.click(); await flush();
  const posted = h.calls.find((c) => c.config.method === "POST");
  assert.deepEqual(JSON.parse(posted.config.body), { decision: "approve", verification_code: "CLI-CODE" });
});

test("Hub refuses to render an owner response arriving after logout", async () => {
  let resolveOwner;
  const delayed = new Promise((resolve) => { resolveOwner = resolve; });
  const h = harness({ owner: () => delayed }); await flush();
  h.setIdentity(null);
  h.windowEvents.storage({ key: null }); await flush();
  resolveOwner({ ok: true, status: 200, json: async () => fixture() }); await flush();
  assert.equal(h.map.get("#hub-content").hidden, true);
  assert.equal(h.map.get("#hub-agents").textContent, "");
  assert.equal(h.map.get("#hub-signin").hidden, false);
});

test("Hub checks the current cookie identity before sending a decision", async () => {
  const h = harness(); await flush();
  const elements = descendants(h.map.get("#hub-agents"));
  elements.find((e) => e.tagName === "input").value = "CLI-CODE";
  h.setIdentity({ id: "different_owner" });
  elements.find((e) => e.tagName === "button" && e.textContent === "Approve").click(); await flush();
  assert.equal(h.calls.filter((c) => c.config.method === "POST").length, 0);
});

test("Expired registrations cannot be approved; hidden pages do not poll", async () => {
  const data = fixture(); data.agents[0].expires_at = new Date(Date.now() - 1000).toISOString();
  const h = harness({ data }); await flush();
  assert.match(h.map.get("#hub-agents").textContent, /Expired/);
  assert.equal(descendants(h.map.get("#hub-agents")).filter((e) => e.tagName === "button").length, 0);
  const before = h.calls.length; h.document.hidden = true; h.docEvents.visibilitychange(); h.map.get("#hub-refresh").click(); await flush();
  assert.equal(h.calls.length, before);
});

test("Requests disclose account, operation, untrusted reason, and full operation scope", async () => {
  const data = fixture(); data.agents[0].status = "approved";
  data.requests.push({ id: "grant_a", agent_id: "agent_a", agent_name: "Helper", connection_id: "connection_a", operation: "messages.get", reason: "<script>approve()</script>", status: "pending", created_at: data.agents[0].created_at, expires_at: data.agents[0].expires_at });
  const h = harness({ data }); await flush();
  const rendered = h.map.get("#hub-requests").textContent;
  assert.match(rendered, /work@example.test/); assert.match(rendered, /messages.get/);
  assert.match(rendered, /Agent-provided reason \(untrusted\)/); assert.match(rendered, /<script>approve\(\)<\/script>/);
  assert.match(rendered, /across all resources/);
  assert.equal(descendants(h.map.get("#hub-requests")).find((e) => e.tagName === "button" && e.textContent === "Approve").disabled, false);
});

function submitGoogle(h, provider = "gmail", name = "Work mailbox") {
  h.map.get("#hub-google-provider").value = provider;
  h.map.get("#hub-google-name").value = name;
  h.map.get("#hub-google-form").listeners.submit({ preventDefault() {} });
}

test("Google onboarding includes owner cookies and sends account label only after identity check", async () => {
  const h = harness(); await flush();
  assert.equal(h.map.get("#hub-google-connect").disabled, false);
  submitGoogle(h); await flush();
  const post = h.calls.find((c) => c.url.endsWith("/v1/hub/google/start"));
  assert.equal(post.config.credentials, "include");
  assert.deepEqual(JSON.parse(post.config.body), { provider_id: "gmail", display_name: "Work mailbox" });
  assert.equal(h.calls[h.calls.indexOf(post) - 1].url.endsWith("/v1/me"), true);
  assert.deepEqual(h.navigations, ["https://accounts.google.com/o/oauth2/v2/auth?state=opaque"]);
});

test("Google onboarding rejects non-Google origins, insecure URLs, and embedded credentials", async () => {
  for (const authorizationURL of ["https://attacker.example/", "https://accounts.google.com.attacker.example/", "http://accounts.google.com/", "javascript:alert(1)", "https://user@accounts.google.com/"]) {
    const h = harness({ authorizationURL }); await flush(); submitGoogle(h); await flush();
    assert.equal(h.navigations.length, 0, authorizationURL);
    assert.match(h.map.get("#hub-google-message").textContent, /Navigation was blocked/);
  }
});

test("Google onboarding is hidden when deployment is not configured", async () => {
  const h = harness({ googleConfigured: false }); await flush();
  assert.equal(h.map.get("#hub-google-connect").disabled, true);
  assert.equal(h.map.get("#hub-add-account").hidden, true);
  assert.equal(h.map.get("#hub-connections").textContent, "");
  submitGoogle(h); await flush();
  assert.equal(h.calls.some((c) => c.url.endsWith("/v1/hub/google/start")), false);
});

test("Google onboarding cannot redirect using an authorization response arriving after logout", async () => {
  let resolveStart;
  const pending = new Promise((resolve) => { resolveStart = resolve; });
  const h = harness({ googleStart: () => pending }); await flush(); submitGoogle(h); await flush();
  h.setIdentity(null); h.windowEvents.storage({ key: null }); await flush();
  resolveStart({ ok: true, status: 200, json: async () => ({ authorization_url: "https://accounts.google.com/o/oauth2/v2/auth" }) }); await flush();
  assert.equal(h.navigations.length, 0);
});

test("Agent approvals disclose account discovery and configured connections avoid claiming verified access", async () => {
  const h = harness(); await flush();
  assert.match(h.map.get("#hub-agents").textContent, /discover connected account names and identities for 7 days/);
  assert.match(h.map.get("#hub-connections").textContent, /access not verified/);
});


test("Registry hides unavailable adapters, no-account providers and disconnected accounts", async () => {
  const data = fixture();
  const hidden = integrationRow("gmail", data.connections, { visible: false });
  const unavailable = integrationRow("future-source", [{ ...data.connections[0], id: "future", provider_id: "future-source" }], { provider: { id: "future-source", name: "Future", implementation_status: "not_implemented", operations: [], limitations: [] }, visible: true, connections: [], connected_account_count: 0 });
  const noAccount = integrationRow("google-drive", []);
  const disconnected = integrationRow("telegram-bot", [{ ...data.connections[0], id: "off", provider_id: "telegram-bot", status: "disconnected" }]);
  const h = harness({ registry: { providers: [hidden, unavailable, noAccount, disconnected], feature_flags: { hub_approvals: true, google_oauth: false, push: false } } }); await flush();
  assert.equal(h.map.get("#hub-connections").textContent, "");
  assert.equal(h.map.get("#hub-add-account").hidden, true);
  assert.equal(h.map.get("#hub-connections-section").hidden, true);
  assert.match(h.map.get("#hub-agents").textContent, /Agent ID/);
});

test("Ready accounts render independently and new browser OAuth providers need no frontend provider list", async () => {
  const data = fixture(), second = { ...data.connections[0], id: "second", account_id: "personal@example.test", display_name: "Personal" };
  const registry = registryFixture(data); registry.providers[0] = integrationRow("gmail", [...data.connections, second]);
  registry.providers.push(integrationRow("google-calendar", []));
  const h = harness({ registry }); await flush();
  assert.match(h.map.get("#hub-connections").textContent, /work@example.test/);
  assert.match(h.map.get("#hub-connections").textContent, /personal@example.test/);
  assert.equal(h.map.get("#hub-google-provider").children.some((c) => c.value === "google-calendar"), true);
  submitGoogle(h, "google-calendar", "Work calendar"); await flush();
  const post = h.calls.find((c) => c.url.endsWith("/v1/hub/google/start"));
  assert.equal(JSON.parse(post.config.body).provider_id, "google-calendar");
});

test("Malformed or unknown registry readiness fails closed while request history remains", async () => {
  const data = fixture(); data.agents[0].status = "approved";
  data.requests.push({ id: "old", agent_id: "agent_a", agent_name: "Helper", connection_id: "connection_a", operation: "messages.get", reason: "Read", status: "approved", created_at: data.agents[0].created_at, expires_at: data.agents[0].expires_at });
  for (const registry of [null, {}, { providers: [], feature_flags: { google_oauth: "true" } }, { ...registryFixture(data), providers: [{ ...integrationRow("gmail", data.connections), visible: "true" }] }, { ...registryFixture(data), providers: [{ ...integrationRow("gmail", data.connections), provider: { id: "gmail", name: "Gmail", implementation_status: "unknown", operations: [{ id: "read", read_only: true }], limitations: [] } }] }]) {
    const h = harness({ data, registry }); await flush();
    assert.equal(h.map.get("#hub-connections").textContent, "");
    assert.equal(h.map.get("#hub-add-account").hidden, true);
    assert.match(h.map.get("#hub-requests").textContent, /Revoke access/);
  }
});

test("Registry changes remove vanished sources and onboarding on refresh", async () => {
  const h = harness(); await flush(); assert.match(h.map.get("#hub-connections").textContent, /work@example.test/);
  h.setRegistry({ providers: [], feature_flags: { hub_approvals: true, google_oauth: false, push: false } });
  h.map.get("#hub-refresh").click(); await flush();
  assert.equal(h.map.get("#hub-connections").textContent, ""); assert.equal(h.map.get("#hub-add-account").hidden, true);
});

test("A registry response arriving after logout cannot reveal previous account identities", async () => {
  let resolveRegistry;
  const pending = new Promise((resolve) => { resolveRegistry = resolve; });
  const h = harness({ integrations: () => pending }); await flush();
  h.setIdentity(null); h.windowEvents.storage({ key: null }); await flush();
  resolveRegistry({ ok: true, status: 200, json: async () => registryFixture(fixture()) }); await flush();
  assert.equal(h.map.get("#hub-connections").textContent, ""); assert.equal(h.map.get("#hub-add-account").hidden, true);
});


function calendarFixture(constraints, provider = "google-calendar", operation = "events.list") {
  const data = fixture(); data.agents[0].status = "approved"; data.connections[0].provider_id = provider;
  data.requests.push({ id: "calendar_grant", agent_id: "agent_a", agent_name: "Helper", connection_id: "connection_a", operation, reason: "Plan my week", status: "pending", created_at: data.agents[0].created_at, expires_at: data.agents[0].expires_at, constraints });
  return data;
}
const calendarBounds = { calendar_id: "work-calendar", time_min: "2026-09-19T00:00:00+08:00", time_max: "2026-09-26T00:00:00+08:00" };

test("Calendar approval displays exact calendar and bounded basic-field scope without broad account grant copy", async () => {
  for (const provider of ["google-calendar", "microsoft-calendar"]) {
    const data = calendarFixture(calendarBounds, provider), h = harness({ data, registry: { providers: [], feature_flags: { hub_approvals: true, google_oauth: false, push: false } } }); await flush();
    const copy = h.map.get("#hub-requests").textContent;
    assert.match(copy, /work-calendar/); assert.match(copy, /2026-09-19T00:00:00\+08:00/); assert.match(copy, /basic event or availability fields only/); assert.doesNotMatch(copy, /across all resources/);
    assert.equal(descendants(h.map.get("#hub-requests")).find((e) => e.tagName === "button" && e.textContent === "Approve").disabled, false);
  }
});

test("Unknown, missing, malformed, zoneless or overlong calendar constraints cannot be approved", async () => {
  const cases = [undefined, null, {}, [], { ...calendarBounds, secret: "extra" }, { ...calendarBounds, time_min: "2026-09-19T00:00:00" }, { ...calendarBounds, time_max: "2026-09-27T00:00:00+08:00" }, { ...calendarBounds, time_max: calendarBounds.time_min }, { ...calendarBounds, time_min: "2026-02-30T00:00:00Z" }];
  for (const constraints of cases) {
    const h = harness({ data: calendarFixture(constraints), registry: null }); await flush();
    const actions = descendants(h.map.get("#hub-requests")).filter((e) => e.tagName === "button");
    assert.equal(actions.find((e) => e.textContent === "Approve").disabled, true);
    assert.equal(Boolean(actions.find((e) => e.textContent === "Deny").disabled), false);
    assert.match(h.map.get("#hub-requests").textContent, /invalid access constraints/);
  }
  const h = harness({ data: calendarFixture(calendarBounds, "gmail", "messages.get"), registry: null }); await flush();
  assert.equal(descendants(h.map.get("#hub-requests")).find((e) => e.tagName === "button" && e.textContent === "Approve").disabled, true);
});


test("Configured CLI accounts remain visible without OAuth, but accounts without API operations stay hidden", async () => {
  const data = fixture(), row = integrationRow("gmail", data.connections, { onboarding_method: "owner_cli" });
  const h = harness({ registry: { providers: [row], feature_flags: { hub_approvals: true, google_oauth: false, push: false } } }); await flush();
  assert.match(h.map.get("#hub-connections").textContent, /work@example.test/);
  assert.equal(h.map.get("#hub-add-account").hidden, true);
  row.connections[0].operations = [];
  h.map.get("#hub-refresh").click(); await flush();
  assert.equal(h.map.get("#hub-connections").textContent, "");
  assert.equal(h.map.get("#hub-connections-section").hidden, true);
});
