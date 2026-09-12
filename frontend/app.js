const API = new URLSearchParams(window.location.search).get("api") || ((window.location.hostname === "127.0.0.1" || window.location.hostname === "localhost") ? "http://127.0.0.1:8401" : "https://context-api.integ.life");
const TOKEN_KEY = "event-context.token";
const USER_KEY = "event-context.user";
const LOCALE_KEY = "event-context.locale";
const SHARED_LOCALE_COOKIE = "event_context_locale";
const { normalizeLocale, resolveLocale, translate } = window.ContextI18n;

function storedUser() {
  try { return JSON.parse(localStorage.getItem(USER_KEY) || "null"); } catch { return null; }
}

function sharedLocale() {
  const prefix = `${SHARED_LOCALE_COOKIE}=`;
  const value = document.cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(prefix));
  if (!value) return "";
  try { return decodeURIComponent(value.slice(prefix.length)); } catch { return ""; }
}

function persistLocale(locale) {
  localStorage.setItem(LOCALE_KEY, locale);
  const attributes = [`${SHARED_LOCALE_COOKIE}=${encodeURIComponent(locale)}`, "Max-Age=31536000", "Path=/", "SameSite=Lax"];
  if (window.location.protocol === "https:") attributes.push("Secure");
  if (window.location.hostname === "integ.life" || window.location.hostname.endsWith(".integ.life")) attributes.push("Domain=.integ.life");
  document.cookie = attributes.join("; ");
}

const systemLanguages = navigator.languages?.length ? navigator.languages : [navigator.language];
const savedLocale = normalizeLocale(sharedLocale()) || normalizeLocale(localStorage.getItem(LOCALE_KEY));
const state = {
  token: localStorage.getItem(TOKEN_KEY),
  user: storedUser(),
  locale: resolveLocale(savedLocale, systemLanguages),
  projects: [], project: null, metadataFields: [], authMode: "login", contentMode: "text", cursor: "", queryMetadata: {}, events: []
};
const $ = (selector, root = document) => root.querySelector(selector);
const status = $("#connection-status");
const usernamePattern = /^[a-z0-9][a-z0-9_.-]{2,63}$/;

function t(key, variables) { return translate(state.locale, key, variables); }
function byteLength(value) { return new TextEncoder().encode(value).length; }
function setMessage(id, text = "", success = false) { const el = $(id); el.textContent = text; el.classList.toggle("success", success); }
function setBusy(button, busy, idleKey) { button.disabled = busy; button.textContent = t(busy ? "processing" : idleKey); }
function jsonObject(value, labelKey) { try { const parsed = JSON.parse(value || "{}"); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error(); return parsed; } catch { throw new Error(t("jsonObjectRequired", { label: t(labelKey) })); } }
function formatDate(value) { try { return new Intl.DateTimeFormat(state.locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); } catch { return new Date(value).toLocaleString(); } }
function formatNumber(value) { try { return new Intl.NumberFormat(state.locale).format(value); } catch { return String(value); } }

function apiError(payload, statusCode, path) {
  const code = payload?.error?.code;
  const message = payload?.error?.message || "";
  if (message.includes("username must be")) return new Error(t("invalidUsername"));
  if (message.includes("password must be")) return new Error(t("invalidPassword"));
  if (message.includes("name required")) return new Error(t("invalidProject"));
  if (code === "unauthenticated") return new Error(t("invalidCredentials"));
  if (code === "forbidden") return new Error(t("forbidden"));
  if (code === "not_found") return new Error(t("notFound"));
  if (code === "conflict" && path === "/v1/auth/register") return new Error(t("usernameExists"));
  if (code === "conflict") return new Error(t("conflict"));
  if (code === "invalid_input") return new Error(t("invalidInput"));
  return new Error(t("requestFailed", { status: statusCode }));
}

async function request(path, { method = "GET", body, auth = true } = {}) {
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (auth && state.token) headers.Authorization = `Bearer ${state.token}`;
  let response;
  try { response = await fetch(`${API}${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) }); }
  catch { throw new Error(t("networkError")); }
  const payload = response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) throw apiError(payload, response.status, path);
  return payload;
}

function applyStaticTranslations() {
  document.documentElement.lang = state.locale;
  document.title = t("documentTitle");
  document.querySelectorAll("[data-i18n]").forEach((element) => { element.textContent = t(element.dataset.i18n); });
  document.querySelectorAll("[data-i18n-placeholder]").forEach((element) => { element.placeholder = t(element.dataset.i18nPlaceholder); });
  document.querySelectorAll("[data-i18n-aria-label]").forEach((element) => { element.setAttribute("aria-label", t(element.dataset.i18nAriaLabel)); });
  document.querySelectorAll("[data-i18n-content]").forEach((element) => { element.setAttribute("content", t(element.dataset.i18nContent)); });
  $("#language-select").value = state.locale;
}

function setLocale(locale, persist = false) {
  state.locale = normalizeLocale(locale) || "en";
  if (persist) persistLocale(state.locale);
  applyStaticTranslations();
  updateIdentity();
  setAuthMode(false);
  setContentMode();
  renderProjects();
  renderProject();
  renderEvents();
  renderMetadata();
}

function saveSession(login) { state.token = login.token; state.user = login.user; localStorage.setItem(TOKEN_KEY, login.token); localStorage.setItem(USER_KEY, JSON.stringify(login.user)); }
function clearSession() { state.token = null; state.user = null; state.projects = []; state.project = null; state.metadataFields = []; localStorage.removeItem(TOKEN_KEY); localStorage.removeItem(USER_KEY); }
function updateIdentity() { const online = Boolean(state.user); status.textContent = online ? t("signedInAs", { username: state.user.username }) : t("signedOut"); status.classList.toggle("online", online); $("#logout-button").classList.toggle("hidden", !online); $("#auth-panel").classList.toggle("hidden", online); $("#workspace").classList.toggle("hidden", !online); }

async function loadProjects(selectID) {
  state.projects = (await request("/v1/projects")).projects;
  const candidate = state.projects.find((project) => project.id === selectID) || state.projects.find((project) => project.id === state.project?.id) || state.projects[0] || null;
  state.project = candidate;
  renderProjects();
  if (candidate) await selectProject(candidate.id); else renderProject();
}

function renderProjects() {
  const list = $("#project-list"); list.replaceChildren();
  for (const project of state.projects) {
    const button = document.createElement("button"); button.type = "button"; button.className = `project-item${project.id === state.project?.id ? " selected" : ""}`;
    const name = document.createElement("span"); name.textContent = project.name;
    const description = document.createElement("small"); description.textContent = project.description || t("noDescription");
    button.append(name, description); button.addEventListener("click", () => selectProject(project.id)); list.append(button);
  }
}

async function selectProject(id) {
  state.project = state.projects.find((project) => project.id === id) || null;
  state.cursor = ""; state.events = []; state.metadataFields = []; state.queryMetadata = {};
  $("#query-metadata").value = ""; renderProjects(); renderProject();
  if (!state.project) return;
  await Promise.all([loadEvents(), loadMetadata()]);
}

function renderProject() {
  const open = Boolean(state.project); $("#empty-project").classList.toggle("hidden", open); $("#project-view").classList.toggle("hidden", !open);
  if (!open) return;
  $("#project-title").textContent = state.project.name;
  $("#project-description").textContent = state.project.description || t("noProjectDescription");
  $("#project-owner").textContent = t("owner", { id: state.project.owner_user_id });
}

async function loadEvents({ more = false } = {}) {
  if (!state.project) return;
  const events = await request("/v1/events/query", { method: "POST", body: { project_id: state.project.id, metadata: state.queryMetadata, limit: 30, cursor: more ? state.cursor : "" } });
  state.events = more ? [...state.events, ...events.events] : events.events;
  state.cursor = events.next_cursor || ""; renderEvents();
}

function renderEvents() {
  const list = $("#events-list"); list.replaceChildren();
  if (!state.events.length) { const empty = document.createElement("p"); empty.className = "empty-events"; empty.textContent = t("noMatchingEvents"); list.append(empty); }
  const template = $("#event-template");
  for (const event of state.events) {
    const node = template.content.firstElementChild.cloneNode(true);
    $(".event-author", node).textContent = event.actor_username;
    const time = $(".event-time", node); time.dateTime = event.recorded_at; time.textContent = formatDate(event.recorded_at);
    $(".event-kind", node).textContent = event.content.kind === "file" ? t("contentKindFile") : t("contentKindText");
    if (event.content.kind === "text") $(".event-text", node).textContent = event.content.text;
    else { $(".event-text", node).classList.add("hidden"); const file = $(".event-file", node); file.classList.remove("hidden"); file.textContent = t("downloadFile", { filename: event.content.file.filename, mediaType: event.content.file.media_type }); file.addEventListener("click", (click) => downloadFile(click, event.content.file)); }
    $(".event-json", node).textContent = JSON.stringify(event.metadata, null, 2);
    list.append(node);
  }
  $("#load-more").classList.toggle("hidden", !state.cursor);
}

async function downloadFile(click, file) {
  click.preventDefault();
  try {
    const payload = await request(`/v1/files/${encodeURIComponent(file.id)}`);
    const bytes = Uint8Array.from(atob(payload.data_base64), (char) => char.charCodeAt(0));
    const url = URL.createObjectURL(new Blob([bytes], { type: payload.file.media_type }));
    const anchor = document.createElement("a"); anchor.href = url; anchor.download = payload.file.filename; anchor.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) { alert(error.message); }
}

function renderMetadata() {
  const list = $("#metadata-list"); list.replaceChildren();
  if (!state.metadataFields.length) { const message = document.createElement("p"); message.className = "muted"; message.textContent = t("metadataNone"); list.append(message); return; }
  for (const field of state.metadataFields) { const item = document.createElement("span"); item.className = "metadata-item"; const key = document.createElement("strong"); key.textContent = field.key; item.append(key, document.createTextNode(` · ${field.types.join("/")} · ${t("metadataEventCount", { count: formatNumber(field.event_count) })}`)); list.append(item); }
}

async function loadMetadata() {
  if (!state.project) return;
  const payload = await request("/v1/metadata/query", { method: "POST", body: { project_id: state.project.id, limit: 100 } });
  state.metadataFields = payload.fields || [];
  renderMetadata();
}

function validateCredentials(credentials, register) {
  if (!usernamePattern.test(credentials.username)) throw new Error(t("invalidUsername"));
  if (register && (byteLength(credentials.password) < 12 || byteLength(credentials.password) > 72)) throw new Error(t("invalidPassword"));
  if (!register && !credentials.password) throw new Error(t("invalidCredentials"));
}

$("#auth-form").addEventListener("submit", async (event) => {
  event.preventDefault(); const submit = $("#auth-submit"); setMessage("#auth-message"); setBusy(submit, true, state.authMode);
  try {
    const credentials = { username: $("#auth-username").value.trim().toLowerCase(), password: $("#auth-password").value };
    validateCredentials(credentials, state.authMode === "register");
    if (state.authMode === "register") {
      await request("/v1/auth/register", { method: "POST", body: credentials, auth: false });
      state.authMode = "login"; setAuthMode(); setMessage("#auth-message", t("registrationSuccess"), true);
    } else { saveSession(await request("/v1/auth/login", { method: "POST", body: credentials, auth: false })); updateIdentity(); await loadProjects(); }
  } catch (error) { setMessage("#auth-message", error.message); }
  finally { setBusy(submit, false, state.authMode); }
});

function setAuthMode(clearMessage = true) { document.querySelectorAll("[data-auth-mode]").forEach((button) => button.classList.toggle("selected", button.dataset.authMode === state.authMode)); $("#auth-submit").textContent = t(state.authMode); $("#auth-password").autocomplete = state.authMode === "login" ? "current-password" : "new-password"; if (clearMessage) setMessage("#auth-message"); }
document.querySelectorAll("[data-auth-mode]").forEach((button) => button.addEventListener("click", () => { state.authMode = button.dataset.authMode; setAuthMode(); }));
$("#language-select").addEventListener("change", (event) => { setMessage("#auth-message"); setMessage("#record-message"); setLocale(event.target.value, true); });
$("#logout-button").addEventListener("click", async () => { try { await request("/v1/auth/logout", { method: "POST", body: {} }); } finally { clearSession(); updateIdentity(); renderProjects(); renderProject(); renderEvents(); renderMetadata(); } });

function showProjectForm(show) { $("#project-form").classList.toggle("hidden", !show); if (show) $("#project-name").focus(); }
$("#new-project-button").addEventListener("click", () => showProjectForm(true)); document.querySelector("[data-open-project-form]").addEventListener("click", () => showProjectForm(true)); $("#cancel-project").addEventListener("click", () => showProjectForm(false));
$("#project-form").addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget; const button = form.querySelector("button[type=submit]"); setBusy(button, true, "create"); try { const name = $("#project-name").value.trim(); const description = $("#project-description").value.trim(); if (!name || byteLength(name) > 200 || byteLength(description) > 4000) throw new Error(t("invalidProject")); const project = await request("/v1/projects", { method: "POST", body: { name, description } }); form.reset(); showProjectForm(false); await loadProjects(project.id); } catch (error) { alert(error.message); } finally { setBusy(button, false, "create"); } });

function setContentMode() { document.querySelectorAll("[data-content-mode]").forEach((button) => button.classList.toggle("selected", button.dataset.contentMode === state.contentMode)); $("#text-input-label").classList.toggle("hidden", state.contentMode !== "text"); $("#file-input-label").classList.toggle("hidden", state.contentMode !== "file"); $("#file-type-label").classList.toggle("hidden", state.contentMode !== "file"); }
document.querySelectorAll("[data-content-mode]").forEach((button) => button.addEventListener("click", () => { state.contentMode = button.dataset.contentMode; setContentMode(); }));
$("#record-form").addEventListener("submit", async (event) => { event.preventDefault(); const button = event.currentTarget.querySelector("button[type=submit]"); setMessage("#record-message"); setBusy(button, true, "appendEvent"); try { const metadata = jsonObject($("#event-metadata").value, "metadataLabel"); let content; if (state.contentMode === "text") { const text = $("#event-text").value; if (!text) throw new Error(t("enterContent")); content = { kind: "text", text }; } else { const input = $("#event-file"); const file = input.files[0]; const mediaType = $("#event-file-type").value.trim(); if (!file || !mediaType) throw new Error(t("selectFileAndType")); const bytes = new Uint8Array(await file.arrayBuffer()); if (bytes.length > 1048576) throw new Error(t("fileTooLarge")); content = { kind: "file", file: { filename: file.name, media_type: mediaType, data_base64: btoa(String.fromCharCode(...bytes)) } }; }
    const localTime = $("#event-occurred-at").value; const payload = { project_id: state.project.id, content, metadata, idempotency_key: crypto.randomUUID() }; if (localTime) payload.occurred_at = new Date(localTime).toISOString(); await request("/v1/events", { method: "POST", body: payload }); $("#event-text").value = ""; $("#event-file").value = ""; $("#event-metadata").value = "{}"; $("#event-occurred-at").value = ""; setMessage("#record-message", t("eventAppended"), true); await Promise.all([loadEvents(), loadMetadata()]);
  } catch (error) { setMessage("#record-message", error.message); } finally { setBusy(button, false, "appendEvent"); } });
$("#query-form").addEventListener("submit", async (event) => { event.preventDefault(); try { state.queryMetadata = jsonObject($("#query-metadata").value, "filterCriteriaLabel"); await loadEvents(); } catch (error) { alert(error.message); } });
$("#clear-query").addEventListener("click", async () => { $("#query-metadata").value = ""; state.queryMetadata = {}; await loadEvents(); }); $("#load-more").addEventListener("click", () => loadEvents({ more: true })); $("#refresh-button").addEventListener("click", () => Promise.all([loadProjects(state.project?.id), loadEvents(), loadMetadata()])); $("#metadata-refresh").addEventListener("click", loadMetadata);

async function boot() { setLocale(state.locale); if (!state.token) return; try { state.user = await request("/v1/me"); localStorage.setItem(USER_KEY, JSON.stringify(state.user)); updateIdentity(); await loadProjects(); } catch { clearSession(); updateIdentity(); setMessage("#auth-message", t("sessionExpired")); } }
boot();
