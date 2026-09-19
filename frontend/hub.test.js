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
      if (url.endsWith("/v1/hub/google/status")) return { ok: true, status: 200, json: async () => ({ configured: options.googleConfigured !== false }) };
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
  return { map, calls, navigations, storage, windowEvents, docEvents, document, setIdentity: (next) => { identity = next; }, setData: (next) => { data = next; } };
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

test("Google onboarding stays disabled and truthful when OAuth is not configured", async () => {
  const h = harness({ googleConfigured: false }); await flush();
  assert.equal(h.map.get("#hub-google-connect").disabled, true);
  assert.match(h.map.get("#hub-google-message").textContent, /not configured/);
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
