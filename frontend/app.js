const { resolveAPI, sessionStorageKey, audioMediaType, pathWithLocale, uuidV7 } = window.ContextWorkspaceUtils;
const { normalizeLocale, resolveLocalePreference, translate } = window.ContextI18n;
const API = resolveAPI(window.location.hostname, new URLSearchParams(window.location.search).get("api"));
const TOKEN_KEY = sessionStorageKey("event-context.token", API);
const USER_KEY = sessionStorageKey("event-context.user", API);
const LOCALE_KEY = "event-context.locale";
const SHARED_LOCALE_COOKIE = "event_context_locale";
const MAX_FILE_BYTES = 50 << 20;
const VIEWS = new Set(["records", "state", "integration", "plugins"]);
const BROWSER_TIMEZONE = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
const $ = (selector, root = document) => root.querySelector(selector);

function storedUser() {
  try { return JSON.parse(localStorage.getItem(USER_KEY) || "null"); } catch { return null; }
}
function sharedLocale() {
  const prefix = SHARED_LOCALE_COOKIE + "=";
  const part = document.cookie.split(";").map((value) => value.trim()).find((value) => value.startsWith(prefix));
  if (!part) return "";
  try { return decodeURIComponent(part.slice(prefix.length)); } catch { return ""; }
}
function persistLocale(locale) {
  localStorage.setItem(LOCALE_KEY, locale);
  const attributes = [SHARED_LOCALE_COOKIE + "=" + encodeURIComponent(locale), "Max-Age=31536000", "Path=/", "SameSite=Lax"];
  if (location.protocol === "https:") attributes.push("Secure");
  if (location.hostname === "integ.life" || location.hostname.endsWith(".integ.life")) attributes.push("Domain=.integ.life");
  document.cookie = attributes.join("; ");
}

const requestedLocale = normalizeLocale(new URLSearchParams(location.search).get("locale"));
const state = {
  token: localStorage.getItem(TOKEN_KEY),
  user: storedUser(),
  locale: resolveLocalePreference(requestedLocale, sharedLocale(), localStorage.getItem(LOCALE_KEY), navigator.languages || [navigator.language]),
  view: VIEWS.has(location.hash.slice(1)) ? location.hash.slice(1) : "records",
  routeProjectID: new URLSearchParams(location.search).get("project") || "", routeError: false,
  projects: [], project: null, projectsStatus: "idle", projectsError: "", projectsRequest: 0,
  sessionEpoch: 0, projectVersion: 0,
  contentMode: "text", pendingEventID: "",
  query: { types: [], metadataRaw: "{}", source: {}, refs_to: "" },
  events: [], cursor: "", latestSequence: 0, eventsStatus: "idle", eventsError: "", eventsRequest: 0,
  metadataFields: [], metadataStatus: "idle", metadataError: "", metadataRequest: 0,
  states: [], statesStatus: "idle", statesError: "", statesRequest: 0,
  members: [], membersStatus: "idle", membersError: "", membersRequest: 0,
  plugins: [], pluginsStatus: "idle", pluginsError: "", pluginsRequest: 0,
  eventCache: new Map(), pluginToken: "",
  audioStatus: { key: "audioNotSelected", variables: {}, success: false }
};
const returnedFromCentralAuth = new URLSearchParams(location.search).get("auth") === "complete";
if (returnedFromCentralAuth) {
  state.token = null; state.user = null;
  localStorage.removeItem(TOKEN_KEY); localStorage.removeItem(USER_KEY);
  const cleanURL = new URL(location.href); cleanURL.searchParams.delete("auth");
  history.replaceState(history.state, "", cleanURL.pathname + cleanURL.search + cleanURL.hash);
}
if (requestedLocale) persistLocale(requestedLocale);

function t(key, variables) { return translate(state.locale, key, variables); }
function byteLength(value) { return new TextEncoder().encode(value).length; }
function formatDate(value) {
  try { return new Intl.DateTimeFormat(state.locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
  catch { return String(value || ""); }
}
function formatNumber(value) {
  try { return new Intl.NumberFormat(state.locale).format(value); } catch { return String(value); }
}
function formatReviewDate(value) {
  try { return new Intl.DateTimeFormat(state.locale, { dateStyle: "long", timeZone: "UTC" }).format(new Date(value + "T12:00:00Z")); }
  catch { return value; }
}
function populateTimezones(select, selected) {
  const available = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  const zones = Array.from(new Set([selected || BROWSER_TIMEZONE, BROWSER_TIMEZONE, "UTC"].concat(available))).filter(Boolean).sort();
  select.replaceChildren();
  for (const zone of zones) {
    const option = document.createElement("option"); option.value = zone; option.textContent = zone; select.append(option);
  }
  select.value = selected || BROWSER_TIMEZONE;
}
function setMessage(selector, text, success) {
  const element = $(selector);
  if (!element) return;
  element.textContent = text || "";
  element.classList.toggle("success", Boolean(success));
}
function setBusy(button, busy, idleKey) {
  button.disabled = busy;
  button.textContent = t(busy ? "processing" : idleKey);
}
function activeProject(version, projectID) {
  return state.projectVersion === version && state.project && state.project.id === projectID;
}
function projectOwnerIDs(project) {
  if (!project) return [];
  if (Array.isArray(project.owner_user_ids) && project.owner_user_ids.length) return project.owner_user_ids;
  return project.owner_user_id ? [project.owner_user_id] : [];
}
function currentUserIsOwner() {
  return Boolean(state.project && state.user && projectOwnerIDs(state.project).includes(state.user.id));
}
function activeSession(epoch, token) { return state.sessionEpoch === epoch && state.token === token; }
function projectPath(suffix) {
  return "/v1/projects/" + encodeURIComponent(state.project.id) + suffix;
}
function jsonLiteral(value, labelKey, array) {
  const raw = String(value || (array ? "[]" : "{}")).trim();
  let parsed;
  try { parsed = JSON.parse(raw); } catch { throw new Error(t("jsonObjectRequired", { label: t(labelKey) })); }
  if (array ? !Array.isArray(parsed) : (!parsed || Array.isArray(parsed) || typeof parsed !== "object")) {
    throw new Error(t("jsonObjectRequired", { label: t(labelKey) }));
  }
  return { raw, parsed };
}
function jsonWithRaw(properties, rawFields) {
  const parts = Object.entries(properties).map(([key, value]) => JSON.stringify(key) + ":" + JSON.stringify(value));
  for (const [key, raw] of Object.entries(rawFields || {})) parts.push(JSON.stringify(key) + ":" + raw);
  return "{" + parts.join(",") + "}";
}

function apiError(payload, statusCode) {
  const code = payload && payload.error && payload.error.code;
  const serviceMessage = payload && payload.error && payload.error.message;
  const key = {
    unauthenticated: "sessionExpired", forbidden: "forbidden", not_found: "notFound",
    conflict: "conflict", invalid_input: "invalidInput", too_large: "fileTooLargeV2",
    invalid_ref: "invalidRef", unsupported_media_type: "unsupportedMediaType",
    state_version_mismatch: "stateVersionMismatch", forbidden_namespace: "forbiddenNamespace",
    plugin_paused: "pluginPaused", rate_limited: "rateLimited"
  }[code];
  const error = new Error(key ? t(key) : (serviceMessage || t("requestFailed", { status: statusCode })));
  error.status = statusCode;
  error.code = code || "";
  return error;
}
async function request(path, options) {
  const config = options || {};
  const headers = {};
  if (config.body !== undefined || config.rawBody !== undefined) headers["Content-Type"] = "application/json";
  if (config.auth !== false && state.token) headers.Authorization = "Bearer " + state.token;
  let response;
  try {
    response = await fetch(API + path, {
      method: config.method || "GET",
      headers,
      credentials: "include",
      body: config.form || config.rawBody || (config.body === undefined ? undefined : JSON.stringify(config.body))
    });
  } catch { throw new Error(t("networkError")); }
  if (!response.ok) throw apiError(await response.json().catch(() => null), response.status);
  if (response.status === 204) return null;
  if (config.responseType === "blob") return response.blob();
  return response.json().catch(() => null);
}

function applyStaticTranslations() {
  document.documentElement.lang = state.locale;
  document.title = t("documentTitle");
  document.querySelectorAll("[data-i18n]").forEach((element) => { element.textContent = t(element.dataset.i18n); });
  document.querySelectorAll("[data-i18n-placeholder]").forEach((element) => { element.placeholder = t(element.dataset.i18nPlaceholder); });
  document.querySelectorAll("[data-i18n-aria-label]").forEach((element) => { element.setAttribute("aria-label", t(element.dataset.i18nAriaLabel)); });
  $("#language-select").value = state.locale;
  updateHomeLink();
}
function setLocale(locale, persist) {
  state.locale = normalizeLocale(locale) || "en";
  if (persist) {
    persistLocale(state.locale);
    history.replaceState(history.state, "", pathWithLocale(window.location.href, state.locale));
  }
  applyStaticTranslations();
  updateIdentity();
  renderProjects();
  renderProject();
  renderEvents();
  renderMetadata();
  renderStates();
  renderMembers();
  renderPlugins();
  renderIntegration();
  renderAudioStatus();
}
function updateHomeLink() {
  const href = window.ContextNavigation.homeURL(location.href, state.locale, true);
  $("#about-link").href = href;
  $(".brand").href = href;
}
function syncRoute(mode = "replace") {
  if (mode === "none") { updateHomeLink(); return; }
  const target = new URL(location.href);
  if (state.routeProjectID) target.searchParams.set("project", state.routeProjectID);
  else target.searchParams.delete("project");
  target.hash = state.view;
  const path = target.pathname + target.search + target.hash;
  if (path !== location.pathname + location.search + location.hash) history[mode === "push" ? "pushState" : "replaceState"](null, "", path);
  updateHomeLink();
}
function restoreRoute() {
  const id = new URLSearchParams(location.search).get("project") || "";
  setView(location.hash.slice(1), "none");
  state.routeProjectID = id;
  if (state.projectsStatus !== "ready") return;
  const target = id || (state.projects[0] && state.projects[0].id) || "";
  if (target !== (state.project && state.project.id)) selectProject(target, "none");
  if (!id && target) { state.routeProjectID = target; syncRoute(); }
}
function setView(view, historyMode = "replace") {
  state.view = VIEWS.has(view) ? view : "records";
  document.querySelectorAll("[data-workspace-view]").forEach((button) => {
    const selected = button.dataset.workspaceView === state.view;
    button.classList.toggle("selected", selected);
    button.setAttribute("aria-selected", String(selected));
  });
  document.querySelectorAll("[data-workspace-panel]").forEach((panel) => panel.classList.toggle("hidden", panel.dataset.workspacePanel !== state.view));
  syncRoute(historyMode);
}

function clearSession() {
  state.sessionEpoch += 1;
  state.projectsRequest += 1;
  state.eventsRequest += 1;
  state.metadataRequest += 1;
  state.statesRequest += 1;
  state.membersRequest += 1;
  state.pluginsRequest += 1;
  state.token = null; state.user = null; state.projects = []; state.project = null;
  state.events = []; state.states = []; state.members = []; state.plugins = []; state.eventCache.clear();
  state.projectVersion += 1;
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
}
function updateIdentity() {
  const online = Boolean(state.user);
  $("#connection-status").textContent = online ? t("signedInAs", { username: state.user.email || state.user.username }) : t("signedOut");
  $("#connection-status").classList.toggle("online", online);
  $("#logout-button").classList.toggle("hidden", !online);
  $("#auth-panel").classList.toggle("hidden", online);
  $("#workspace").classList.toggle("hidden", !online);
}

async function loadProjects(selectID) {
  const epoch = state.sessionEpoch, token = state.token, requestVersion = ++state.projectsRequest;
  state.projectsStatus = "loading"; state.projectsError = ""; renderProjects(); renderProject();
  try {
    const projects = (await request("/v1/projects")).projects || [];
    if (!activeSession(epoch, token) || requestVersion !== state.projectsRequest) return;
    state.projects = projects; state.projectsStatus = "ready";
    renderProjects();
    const requestedID = selectID || new URLSearchParams(location.search).get("project") || "";
    await selectProject(requestedID || (projects[0] && projects[0].id) || "", selectID ? "push" : "replace");
  } catch (error) {
    if (!activeSession(epoch, token) || requestVersion !== state.projectsRequest) return;
    state.projectsStatus = "error"; state.projectsError = error.message; renderProjects(); renderProject();
  }
}
function renderProjects() {
  const list = $("#project-list"); list.replaceChildren();
  if (state.projectsStatus === "loading") {
    const message = document.createElement("p"); message.className = "muted"; message.textContent = t("loadingProjects"); list.append(message);
  }
  if (state.projectsStatus === "error") {
    const message = document.createElement("p"); message.className = "message"; message.textContent = t("projectsLoadFailed", { error: state.projectsError }); list.append(message);
  }
  for (const project of state.projects) {
    const button = document.createElement("button"); button.type = "button"; button.className = "project-item" + (project.id === (state.project && state.project.id) ? " selected" : "");
    const name = document.createElement("span"); name.textContent = project.name;
    const description = document.createElement("small"); description.textContent = project.description || t("noDescription");
    button.append(name, description); button.addEventListener("click", () => selectProject(project.id)); list.append(button);
  }
}
async function selectProject(id, historyMode = "push") {
  state.routeProjectID = id;
  state.project = state.projects.find((project) => project.id === id) || null;
  state.routeError = Boolean(id && !state.project);
  syncRoute(historyMode);
  state.projectVersion += 1;
  setMessage("#record-message");
  setMessage("#members-message");
  $("#add-member-form").reset();
  state.pendingEventID = ""; state.cursor = ""; state.events = []; state.states = []; state.members = []; state.plugins = []; state.eventCache.clear();
  state.eventsStatus = state.metadataStatus = state.statesStatus = state.membersStatus = state.pluginsStatus = "loading";
  renderProjects(); renderProject(); renderEvents(); renderMetadata(); renderStates(); renderMembers(); renderPlugins(); renderIntegration();
  if (!state.project) return;
  await Promise.allSettled([loadEvents(), loadMetadata(), loadStates(), loadMembers(), loadPlugins()]);
}
function renderProject() {
  const open = Boolean(state.project);
  const unavailable = !open && (state.routeError || state.projectsStatus === "loading" || state.projectsStatus === "error");
  const empty = !open && !state.routeError && state.projectsStatus === "ready" && !state.projects.length;
  $("#empty-project").classList.toggle("hidden", !empty);
  $("#project-unavailable").classList.toggle("hidden", !unavailable);
  $("#project-view").classList.toggle("hidden", !open);
  if (unavailable) $("#project-unavailable-message").textContent = state.routeError ? t("projectRouteUnavailable") : state.projectsStatus === "loading" ? t("loadingProjects") : t("projectsLoadFailed", { error: state.projectsError });
  if (!open) return;
  $("#project-title").textContent = state.project.name;
  $("#project-description-display").textContent = state.project.description || t("noProjectDescription");
  $("#project-owner").textContent = t("owners", { ids: projectOwnerIDs(state.project).join(", ") });
  $("#project-timezone-display").textContent = state.project.timezone || "UTC";
  populateTimezones($("#project-timezone-select"), state.project.timezone || "UTC");
  $("#project-timezone-form").classList.add("hidden");
  $("#edit-project-timezone").classList.remove("hidden");
  setMessage("#project-timezone-message");
}

async function loadEvents(options) {
  if (!state.project) return;
  const more = options && options.more;
  const projectID = state.project.id, version = state.projectVersion, requestVersion = ++state.eventsRequest;
  const query = {
    types: state.query.types,
    source: state.query.source,
    refs_to: state.query.refs_to,
    limit: 30,
    cursor: more ? state.cursor : ""
  };
  state.eventsStatus = "loading"; state.eventsError = ""; renderEvents();
  try {
    const rawBody = jsonWithRaw(query, { metadata: state.query.metadataRaw });
    const page = await request(projectPath("/events/query"), { method: "POST", rawBody });
    if (!activeProject(version, projectID) || requestVersion !== state.eventsRequest) return;
    state.events = more ? state.events.concat(page.events || []) : (page.events || []);
    for (const event of state.events) state.eventCache.set(event.id, event);
    state.cursor = page.next_cursor || ""; state.latestSequence = page.latest_sequence || 0; state.eventsStatus = "ready"; renderEvents();
  } catch (error) {
    if (!activeProject(version, projectID) || requestVersion !== state.eventsRequest) return;
    state.eventsStatus = "error"; state.eventsError = error.message; renderEvents();
  }
}
function renderEvents() {
  const list = $("#events-list"); list.replaceChildren();
  setMessage("#events-message", state.eventsStatus === "error" ? state.eventsError : "");
  if (state.eventsStatus === "loading" && !state.events.length) return appendEmpty(list, "loadingRecords");
  if (state.eventsStatus === "ready" && !state.events.length) return appendEmpty(list, "noMatchingEvents");
  for (const event of state.events) list.append(eventNode(event));
  $("#load-more").classList.toggle("hidden", !state.cursor);
}
function eventNode(event, compact) {
  const article = document.createElement("article"); article.className = compact ? "source-panel" : "event";
  const meta = document.createElement("div"); meta.className = "event-meta";
  const actor = document.createElement("strong"); actor.textContent = (event.actor && (event.actor.username || event.actor.id)) || t("unknownActor");
  const time = document.createElement("time"); time.className = "event-time"; time.dateTime = event.recorded_at; time.textContent = formatDate(event.recorded_at);
  const type = document.createElement("span"); type.className = "event-kind"; type.textContent = event.type;
  const source = document.createElement("span"); source.className = "event-kind"; source.textContent = (event.source && event.source.channel) || t("unknownSource");
  meta.append(actor, time, type, source); article.append(meta);
  if (event.content && event.content.kind === "text") {
    const text = document.createElement("p"); text.className = "event-text"; text.textContent = event.content.text; article.append(text);
  } else if (event.content && event.content.file_id) {
    const file = document.createElement("button"); file.type = "button"; file.className = "quiet source-download";
    file.textContent = t("downloadFile", { filename: event.content.filename || event.content.file_id, mediaType: event.content.media_type || "" });
    file.addEventListener("click", () => downloadFile(event.content)); article.append(file);
  }
  if (event.refs && event.refs.length) {
    const refs = document.createElement("div"); refs.className = "reference-list";
    for (const ref of event.refs) appendSourceControl(refs, ref.id, ref.rel);
    article.append(refs);
  }
  const details = document.createElement("details"); details.className = "event-details";
  const summary = document.createElement("summary"); summary.textContent = t("eventDetails");
  const detailBody = document.createElement("pre"); detailBody.className = "event-json";
  detailBody.textContent = JSON.stringify({ id: event.id, sequence: event.sequence, actor: event.actor, recorded_at: event.recorded_at, occurred_at: event.occurred_at, source: event.source, metadata: event.metadata, refs: event.refs }, null, 2);
  details.append(summary, detailBody); article.append(details);
  return article;
}
async function downloadFile(content) {
  try {
    const blob = await request(projectPath("/files/" + encodeURIComponent(content.file_id)), { responseType: "blob" });
    const url = URL.createObjectURL(blob), anchor = document.createElement("a");
    anchor.href = url; anchor.download = content.filename || content.file_id; anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) { setMessage("#events-message", error.message); }
}
function appendSourceControl(root, eventID, relation) {
  const wrap = document.createElement("div"); wrap.className = "source-control";
  const button = document.createElement("button"); button.type = "button"; button.className = "quiet";
  button.textContent = relation ? t("openReferencedEvent", { relation }) : t("showSourceID", { id: shortID(eventID) });
  button.setAttribute("aria-expanded", "false");
  const panel = document.createElement("div"); panel.className = "source-panel hidden";
  button.addEventListener("click", async () => {
    if (!panel.classList.contains("hidden")) { panel.classList.add("hidden"); button.setAttribute("aria-expanded", "false"); return; }
    panel.classList.remove("hidden"); button.setAttribute("aria-expanded", "true"); panel.textContent = t("loadingSource");
    try {
      let event = state.eventCache.get(eventID);
      if (!event) {
        event = await request(projectPath("/events/" + encodeURIComponent(eventID)));
        state.eventCache.set(eventID, event);
      }
      panel.replaceChildren(eventNode(event, true));
    } catch (error) { panel.textContent = t("sourceUnavailable") + " " + error.message; }
  });
  wrap.append(button, panel); root.append(wrap);
}
function shortID(value) { return value && value.length > 14 ? value.slice(0, 7) + "…" + value.slice(-4) : value; }

async function loadMetadata() {
  if (!state.project) return;
  const projectID = state.project.id, version = state.projectVersion, requestVersion = ++state.metadataRequest;
  state.metadataStatus = "loading"; state.metadataError = ""; renderMetadata();
  try {
    const payload = await request(projectPath("/metadata"));
    if (!activeProject(version, projectID) || requestVersion !== state.metadataRequest) return;
    state.metadataFields = payload.fields || []; state.metadataStatus = "ready"; renderMetadata();
  } catch (error) {
    if (!activeProject(version, projectID) || requestVersion !== state.metadataRequest) return;
    state.metadataStatus = "error"; state.metadataError = error.message; renderMetadata();
  }
}
function renderMetadata() {
  const list = $("#metadata-list"); list.replaceChildren();
  if (state.metadataStatus === "loading") return appendEmpty(list, "loadingMetadata");
  if (state.metadataStatus === "error") { const message = document.createElement("p"); message.className = "message"; message.textContent = state.metadataError; list.append(message); return; }
  if (!state.metadataFields.length) return appendEmpty(list, "metadataNone");
  for (const field of state.metadataFields) {
    const item = document.createElement("span"); item.className = "metadata-item";
    item.textContent = field.key + " · " + field.types.join("/") + " · " + t("metadataEventCount", { count: formatNumber(field.event_count) });
    list.append(item);
  }
}

async function loadStates() {
  if (!state.project) return;
  const projectID = state.project.id, version = state.projectVersion, requestVersion = ++state.statesRequest;
  state.statesStatus = "loading"; state.statesError = ""; renderStates();
  try {
    const payload = await request(projectPath("/state"));
    if (!activeProject(version, projectID) || requestVersion !== state.statesRequest) return;
    state.states = payload.states || []; state.latestSequence = payload.latest_sequence || state.latestSequence;
    state.statesStatus = "ready"; renderStates();
  } catch (error) {
    if (!activeProject(version, projectID) || requestVersion !== state.statesRequest) return;
    state.statesStatus = "error"; state.statesError = error.message; renderStates();
  }
}
function renderStates() {
  const list = $("#state-list"); list.replaceChildren();
  setMessage("#state-message", state.statesStatus === "error" ? state.statesError : "");
  if (state.statesStatus === "loading") return appendEmpty(list, "stateLoading");
  if (state.statesStatus === "ready" && !state.states.length) return appendEmpty(list, "stateEmpty");
  for (const item of state.states) list.append(stateNode(item));
}
function stateNode(item, latestVersion) {
  const newestVersion = latestVersion || item.version;
  const article = document.createElement("article"); article.className = "state-card";
  const heading = document.createElement("div"); heading.className = "card-title";
  const dailyDate = item.key && item.key.startsWith("daily-review/") ? item.key.slice("daily-review/".length) : "";
  const isDailyReview = /^\d{4}-\d{2}-\d{2}$/.test(dailyDate);
  const title = document.createElement("h4"); title.textContent = isDailyReview ? t("dailyReviewDate", { date: formatReviewDate(dailyDate) }) : item.key;
  const badges = document.createElement("div"); badges.className = "state-badges";
  for (const label of [t("versionLabel", { version: item.version }), item.lag ? t("laggingLabel", { count: item.lag }) : t("upToDate")]) {
    const badge = document.createElement("span"); badge.textContent = label; badges.append(badge);
  }
  heading.append(title, badges); article.append(heading);
  if (isDailyReview) {
    article.classList.add("daily-review-state");
    const key = document.createElement("p"); key.className = "muted state-key"; key.textContent = item.key; article.append(key);
  }
  const producer = document.createElement("p"); producer.className = "muted";
  producer.textContent = t("stateProducer", { plugin: item.producer && item.producer.plugin_id || "?", version: item.producer && item.producer.plugin_version || "?", date: formatDate(item.updated_at) });
  article.append(producer);
  const content = document.createElement("pre"); content.className = "state-content"; content.textContent = item.content && item.content.text || ""; article.append(content);
  if (item.data !== undefined && item.data !== null) {
    const data = document.createElement("details"), summary = document.createElement("summary"), pre = document.createElement("pre");
    summary.textContent = t("structuredData"); pre.className = "event-json"; pre.textContent = JSON.stringify(item.data, null, 2); data.append(summary, pre); article.append(data);
  }
  if (item.refs && item.refs.length) {
    const refs = document.createElement("div"); refs.className = "reference-list";
    for (const id of item.refs) appendSourceControl(refs, id);
    article.append(refs);
  }
  if (newestVersion > 1) {
    const label = document.createElement("label"); label.className = "history-select"; label.append(document.createTextNode(t("viewVersion")));
    const select = document.createElement("select");
    for (let version = newestVersion; version >= 1; version -= 1) {
      const option = document.createElement("option"); option.value = String(version); option.textContent = t("versionLabel", { version }); select.append(option);
    }
    select.value = String(item.version);
    select.addEventListener("change", () => loadStateVersion(item.key, Number(select.value), article, newestVersion));
    label.append(select); article.append(label);
  }
  return article;
}
async function loadStateVersion(key, version, article, newestVersion) {
  const slash = key.indexOf("/");
  if (slash < 1) return setMessage("#state-message", t("invalidStateKey"));
  const path = "/state/" + encodeURIComponent(key.slice(0, slash)) + "/" + encodeURIComponent(key.slice(slash + 1)) + "?version=" + version;
  setMessage("#state-message", t("stateLoading"));
  try {
    const historical = await request(projectPath(path));
    article.replaceWith(stateNode(historical, newestVersion));
    setMessage("#state-message", t("stateVersionLoaded", { version }), true);
  } catch (error) { setMessage("#state-message", error.message); }
}
function appendEmpty(root, key) {
  const message = document.createElement("p"); message.className = "muted empty-events"; message.textContent = t(key); root.append(message);
}

function renderAudioStatus() {
  const message = $("#audio-local-status");
  if (!message) return;
  message.textContent = t(state.audioStatus.key, state.audioStatus.variables);
  message.classList.toggle("success", state.audioStatus.success);
}
function setAudioStatus(key, variables, success) {
  state.audioStatus = { key, variables: variables || {}, success: Boolean(success) }; renderAudioStatus();
}
function setContentMode() {
  document.querySelectorAll("[data-content-mode]").forEach((button) => button.classList.toggle("selected", button.dataset.contentMode === state.contentMode));
  $("#text-input-label").classList.toggle("hidden", state.contentMode !== "text");
  $("#file-input-label").classList.toggle("hidden", state.contentMode !== "file");
  $("#audio-input-panel").classList.toggle("hidden", state.contentMode !== "audio");
}
async function sha256Hex(file) {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
async function uploadFile(file, projectID) {
  if (!file || file.size > MAX_FILE_BYTES) throw new Error(t("fileTooLargeV2"));
  const form = new FormData(), hash = await sha256Hex(file);
  form.set("sha256", hash); form.set("file", file, file.name);
  const uploaded = await request("/v1/projects/" + encodeURIComponent(projectID) + "/files", { method: "POST", form });
  if (uploaded.sha256 !== hash || uploaded.size_bytes !== file.size) throw new Error(t("fileIntegrityFailed"));
  return uploaded;
}

function resetRecordID() { state.pendingEventID = ""; }
function recordBody(eventBase, metadataRaw, refs) {
  const event = Object.assign({}, eventBase, { refs });
  return "{\"events\":[" + jsonWithRaw(event, { metadata: metadataRaw }) + "]}";
}

async function submitRecord(form) {
  if (!state.project) return;
  const button = form.querySelector("button[type=submit]"), projectID = state.project.id, version = state.projectVersion;
  setMessage("#record-message"); setBusy(button, true, "appendEvent");
  try {
    const metadata = jsonLiteral($("#event-metadata").value, "metadataLabel", false);
    const refs = jsonLiteral($("#event-refs").value, "referencesLabel", true).parsed;
    if (!state.pendingEventID) state.pendingEventID = uuidV7();
    const eventID = state.pendingEventID, contentMode = state.contentMode;
    const eventType = $("#event-type").value, occurredAt = $("#event-occurred-at").value;
    const targetPath = "/v1/projects/" + encodeURIComponent(projectID);
    let content;
    if (contentMode === "text") {
      const text = $("#event-text").value;
      if (!text) throw new Error(t("enterContent"));
      content = { kind: "text", text };
    } else {
      let file = contentMode === "audio" ? $("#audio-file").files[0] : $("#event-file").files[0];
      if (!file) throw new Error(t("selectFile"));
      if (contentMode === "audio") {
        const mediaType = audioMediaType(file.name, file.type);
        if (!mediaType) throw new Error(t("invalidAudioType"));
        if (file.type !== mediaType) file = new File([file], file.name, { type: mediaType, lastModified: file.lastModified });
        setAudioStatus("audioUploading");
      }
      const uploaded = await uploadFile(file, projectID);
      content = { kind: "file", file_id: uploaded.file_id, media_type: uploaded.media_type, filename: uploaded.filename, size_bytes: uploaded.size_bytes, sha256: uploaded.sha256 };
    }
    const eventBase = {
      id: eventID,
      type: eventType,
      content,
      source: { channel: "api", client: "web" }
    };
    if (occurredAt) eventBase.occurred_at = new Date(occurredAt).toISOString();
    const result = await request(targetPath + "/events", { method: "POST", rawBody: recordBody(eventBase, metadata.raw, refs) });
    const outcome = result && result.results && result.results[0];
    if (!outcome || !["created", "duplicate"].includes(outcome.status)) {
      const error = outcome && outcome.error;
      throw new Error(error && error.message || t("eventWriteFailed"));
    }
    if (!activeProject(version, projectID)) return;
    const savedEvent = await request(targetPath + "/events/" + encodeURIComponent(outcome.id));
    if (!activeProject(version, projectID)) return;
    state.eventCache.set(savedEvent.id, savedEvent);
    state.events = [savedEvent].concat(state.events.filter((item) => item.id !== savedEvent.id));
    state.eventsStatus = "ready";
    if (state.pendingEventID === eventID) {
      form.reset(); $("#event-metadata").value = "{}"; $("#event-refs").value = "[]"; state.pendingEventID = "";
      setAudioStatus("audioNotSelected");
    }
    setMessage("#record-message", t(outcome.status === "duplicate" ? "eventDuplicate" : "eventAppended") + " · " + outcome.id, true);
    renderEvents();
    await Promise.allSettled([loadMetadata(), loadStates()]);
  } catch (error) {
    if (activeProject(version, projectID)) {
      setMessage("#record-message", error.message);
      if (state.contentMode === "audio") setAudioStatus("audioUploadFailedRetained");
    }
  } finally { setBusy(button, false, "appendEvent"); }
}

function renderIntegration() {
  if (!state.project) return;
  const projectID = state.project.id;
  const guideURL = new URL("./agent-setup.md", window.location.href);
  guideURL.searchParams.set("project", projectID);
  guideURL.searchParams.set("locale", state.locale);
  const skillURL = new URL("./skills/edc-recorder/SKILL.md", window.location.href).href;
  $("#integration-project-id").textContent = projectID;
  $("#agent-setup-guide-link").href = guideURL.href;
  $("#agent-setup-prompt").textContent = t("agentSetupPrompt", { guide_url: guideURL.href, project_id: projectID, skill_url: skillURL });
  $("#chatgpt-verify-prompt").textContent = t("chatGPTVerifyPrompt", { project_id: projectID });
}
async function loadMembers() {
  if (!state.project) return;
  const projectID = state.project.id, version = state.projectVersion, requestVersion = ++state.membersRequest;
  state.membersStatus = "loading"; state.membersError = ""; renderMembers();
  try {
    const payload = await request(projectPath("/members"));
    if (!activeProject(version, projectID) || requestVersion !== state.membersRequest) return;
    state.members = payload.members || []; state.membersStatus = "ready"; renderMembers();
  } catch (error) {
    if (!activeProject(version, projectID) || requestVersion !== state.membersRequest) return;
    state.membersStatus = "error"; state.membersError = error.message; renderMembers();
  }
}
function renderMembers() {
  const list = $("#members-list");
  if (!list) return;
  list.replaceChildren();
  const owner = currentUserIsOwner();
  const ownerIDs = new Set(projectOwnerIDs(state.project));
  const ownerCount = state.members.filter((member) => member.role === "owner" || ownerIDs.has(member.id)).length;
  $("#add-member-form").classList.toggle("hidden", !owner);
  $("#member-owner-note").classList.toggle("hidden", owner || !state.project);
  setMessage("#members-message", state.membersStatus === "error" ? state.membersError : "");
  if (state.membersStatus === "loading") return appendEmpty(list, "membersLoading");
  if (state.membersStatus === "ready" && !state.members.length) return appendEmpty(list, "membersEmpty");
  for (const member of state.members) {
    const item = document.createElement("div"); item.className = "member-item";
    const username = document.createElement("strong"); username.textContent = "@" + member.username;
    const id = document.createElement("code"); id.textContent = t("memberID", { id: member.id });
    item.append(username, id);
    const memberIsOwner = member.role === "owner" || ownerIDs.has(member.id);
    if (memberIsOwner) {
      const badge = document.createElement("span"); badge.className = "status-badge ready"; badge.textContent = t("projectOwner"); item.append(badge);
    }
    if (owner && state.user && member.id !== state.user.id) {
      const action = document.createElement("button"); action.type = "button"; action.className = "quiet member-role-action";
      action.dataset.memberId = member.id; action.dataset.role = memberIsOwner ? "member" : "owner";
      action.textContent = t(memberIsOwner ? "makeMember" : "makeOwner");
      if (memberIsOwner && ownerCount <= 1) { action.disabled = true; action.title = t("lastOwnerRequired"); }
      item.append(action);
    }
    list.append(item);
  }
}
function renderPlugins() {
  const list = $("#plugins-list"); list.replaceChildren();
  setMessage("#plugins-message", state.pluginsStatus === "error" ? state.pluginsError : "");
  if (state.pluginsStatus === "loading") return appendEmpty(list, "pluginsLoading");
  if (state.pluginsStatus === "ready" && !state.plugins.length) return appendEmpty(list, "pluginsEmpty");
  for (const plugin of state.plugins) {
    const card = document.createElement("article"); card.className = "plugin-card";
    const heading = document.createElement("div"); heading.className = "card-title";
    const title = document.createElement("h4"); title.textContent = plugin.manifest && plugin.manifest.name || plugin.plugin_id;
    const badge = document.createElement("span"); badge.className = "status-badge " + (plugin.status === "active" ? "ready" : "pending"); badge.textContent = t(plugin.status === "active" ? "enabled" : "paused");
    heading.append(title, badge);
    const detail = document.createElement("p"); detail.className = "muted";
    detail.textContent = plugin.plugin_id + " · " + plugin.plugin_version + " · " + t("configRevision", { revision: plugin.config_revision });
    const description = document.createElement("p"); description.textContent = plugin.manifest && plugin.manifest.description || "";
    const permissionDetails = document.createElement("details"), permissionSummary = document.createElement("summary"), permissions = document.createElement("pre");
    permissionSummary.textContent = t("permissionsTitle"); permissions.className = "event-json"; permissions.textContent = JSON.stringify(plugin.permissions, null, 2);
    permissionDetails.append(permissionSummary, permissions);
    const actions = document.createElement("div"); actions.className = "actions";
    const configure = actionButton("editConfig", () => showConfigEditor(card, plugin));
    const toggle = actionButton(plugin.status === "active" ? "pause" : "resume", () => patchPluginStatus(plugin, plugin.status === "active" ? "pause" : "resume", toggle));
    const run = actionButton("manualRun", () => requestPluginRun(plugin, run));
    const remove = actionButton("reviewUninstall", () => showUninstallReview(card, plugin));
    actions.append(configure, toggle, run, remove);
    card.append(heading, detail, description, permissionDetails, actions); list.append(card);
  }
}
function actionButton(key, handler) {
  const button = document.createElement("button"); button.type = "button"; button.className = "quiet"; button.textContent = t(key); button.addEventListener("click", handler); return button;
}
function pluginPath(plugin) { return projectPath("/plugins/" + encodeURIComponent(plugin.plugin_id)); }
async function patchPluginStatus(plugin, action, button) {
  button.disabled = true; setMessage("#plugins-message");
  try {
    await request(pluginPath(plugin), { method: "PATCH", body: { action } });
    await loadPlugins(); setMessage("#plugins-message", t(action === "pause" ? "pluginPausedSuccess" : "pluginResumedSuccess"), true);
  } catch (error) { setMessage("#plugins-message", error.message); button.disabled = false; }
}
function showConfigEditor(card, plugin) {
  const existing = $(".plugin-inline-editor", card);
  if (existing) { existing.remove(); return; }
  const form = document.createElement("form"); form.className = "plugin-inline-editor";
  const label = document.createElement("label"); label.textContent = t("pluginConfig");
  const textarea = document.createElement("textarea"); textarea.rows = 6; textarea.spellcheck = false; textarea.value = JSON.stringify(plugin.config || {}, null, 2); label.append(textarea);
  const note = document.createElement("p"); note.className = "muted"; note.textContent = t("configReviewHint", { revision: plugin.config_revision });
  const actions = document.createElement("div"); actions.className = "actions";
  const cancel = actionButton("cancel", () => form.remove());
  const save = actionButton("saveConfig", async () => {
    let config;
    try { config = jsonLiteral(textarea.value, "pluginConfig", false); } catch (error) { setMessage("#plugins-message", error.message); return; }
    save.disabled = true;
    const rawBody = jsonWithRaw({ action: "config", expected_revision: plugin.config_revision }, { config: config.raw });
    try {
      await request(pluginPath(plugin), { method: "PATCH", rawBody });
      await loadPlugins(); setMessage("#plugins-message", t("configSaved"), true);
    } catch (error) { setMessage("#plugins-message", error.message); save.disabled = false; }
  });
  actions.append(cancel, save); form.append(label, note, actions); card.append(form); textarea.focus();
}
async function requestPluginRun(plugin, button) {
  button.disabled = true; setMessage("#plugins-message");
  try {
    const accepted = await request(pluginPath(plugin) + "/runs", { method: "POST", body: { request_id: uuidV7(), source_event_ids: [] } });
    setMessage("#plugins-message", t("runAccepted", { id: accepted.request_id }), true);
  } catch (error) { setMessage("#plugins-message", error.message); }
  finally { button.disabled = false; }
}
function showUninstallReview(card, plugin) {
  const existing = $(".uninstall-review", card);
  if (existing) { existing.remove(); return; }
  const review = document.createElement("div"); review.className = "uninstall-review";
  const text = document.createElement("p"); text.textContent = t("uninstallReview", { name: plugin.manifest && plugin.manifest.name || plugin.plugin_id });
  const actions = document.createElement("div"); actions.className = "actions";
  const cancel = actionButton("cancel", () => review.remove());
  const remove = actionButton("uninstallAction", async () => {
    remove.disabled = true;
    try {
      await request(pluginPath(plugin), { method: "DELETE" });
      await Promise.allSettled([loadPlugins(), loadStates()]);
      setMessage("#plugins-message", t("pluginUninstalled"), true);
    } catch (error) { setMessage("#plugins-message", error.message); remove.disabled = false; }
  });
  remove.classList.add("danger");
  actions.append(cancel, remove); review.append(text, actions); card.append(review);
}
async function loadPlugins() {
  if (!state.project) return;
  const projectID = state.project.id, version = state.projectVersion, requestVersion = ++state.pluginsRequest;
  state.pluginsStatus = "loading"; state.pluginsError = ""; renderPlugins();
  try {
    const payload = await request(projectPath("/plugins"));
    if (!activeProject(version, projectID) || requestVersion !== state.pluginsRequest) return;
    state.plugins = payload.plugins || []; state.pluginsStatus = "ready"; renderPlugins();
  } catch (error) {
    if (!activeProject(version, projectID) || requestVersion !== state.pluginsRequest) return;
    state.pluginsStatus = "error"; state.pluginsError = error.message; renderPlugins();
  }
}

document.querySelectorAll("[data-workspace-view]").forEach((button) => button.addEventListener("click", () => setView(button.dataset.workspaceView, "push")));
document.querySelectorAll("[data-content-mode]").forEach((button) => button.addEventListener("click", () => { state.contentMode = button.dataset.contentMode; resetRecordID(); setContentMode(); }));
document.querySelectorAll("[data-copy-command]").forEach((button) => button.addEventListener("click", async () => {
  try { await navigator.clipboard.writeText($("#" + button.dataset.copyCommand).textContent); setMessage("#integration-message", t("commandCopied"), true); }
  catch { setMessage("#integration-message", t("copyFailed")); }
}));
$("#language-select").addEventListener("change", (event) => setLocale(event.target.value, true));
$("#auth-submit").addEventListener("click", () => {
  const returnURL = new URL(location.href);
  returnURL.searchParams.delete("auth");
  const returnTo = returnURL.pathname + returnURL.search + returnURL.hash;
  clearSession();
  const start = new URL(API + "/v1/auth/integ/start");
  start.searchParams.set("ui_locales", state.locale);
  start.searchParams.set("return_to", returnTo);
  location.assign(start.toString());
});
$("#logout-button").addEventListener("click", async () => {
  const button = $("#logout-button");
  button.disabled = true;
  try {
    await request("/v1/auth/logout", { method: "POST" });
    clearSession();
    location.replace(window.ContextNavigation.homeURL(location.href, state.locale));
  } catch (error) {
    // Keep the live session on failure so a cookie cannot silently sign back in.
    $("#connection-status").textContent = error.message;
  } finally { button.disabled = false; }
});
function showProjectForm(show) { $("#project-form").classList.toggle("hidden", !show); }
$("#new-project-button").addEventListener("click", () => showProjectForm(true));
$("[data-open-project-form]").addEventListener("click", () => showProjectForm(true));
$("#cancel-project").addEventListener("click", () => showProjectForm(false));
$("#project-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget, button = form.querySelector("button[type=submit]");
  setBusy(button, true, "create");
  try {
    const project = await request("/v1/projects", { method: "POST", body: { name: $("#project-name").value.trim(), description: $("#project-description-input").value.trim(), timezone: $("#project-timezone").value } });
    form.reset(); populateTimezones($("#project-timezone"), BROWSER_TIMEZONE); showProjectForm(false); await loadProjects(project.id);
  } catch (error) { setMessage("#auth-message", error.message); }
  finally { setBusy(button, false, "create"); }
});
$("#record-form").addEventListener("submit", (event) => { event.preventDefault(); submitRecord(event.currentTarget); });
$("#record-form").addEventListener("input", resetRecordID);
$("#record-form").addEventListener("change", resetRecordID);
$("#audio-file").addEventListener("change", () => {
  const file = $("#audio-file").files[0];
  setAudioStatus(file ? "audioSelectedLocal" : "audioNotSelected", file ? { filename: file.name, size: formatNumber(file.size) } : {});
});
$("#query-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    const metadata = jsonLiteral($("#query-metadata").value || "{}", "filterCriteriaLabel", false);
    state.query = {
      types: $("#query-type").value ? [$("#query-type").value] : [],
      metadataRaw: metadata.raw,
      source: $("#query-source").value.trim() ? { channel: $("#query-source").value.trim() } : {},
      refs_to: $("#query-refs").value.trim()
    };
    state.cursor = ""; await loadEvents();
  } catch (error) { setMessage("#events-message", error.message); }
});
$("#clear-query").addEventListener("click", async () => {
  $("#query-form").reset(); state.query = { types: [], metadataRaw: "{}", source: {}, refs_to: "" }; state.cursor = ""; await loadEvents();
});
$("#load-more").addEventListener("click", () => loadEvents({ more: true }));
$("#metadata-refresh").addEventListener("click", loadMetadata);
$("#state-refresh").addEventListener("click", loadStates);
$("#members-refresh").addEventListener("click", loadMembers);
$("#members-list").addEventListener("click", async (event) => {
  const button = event.target.closest("button[data-member-id]");
  if (!button || !state.project) return;
  const projectID = state.project.id, version = state.projectVersion, role = button.dataset.role;
  setBusy(button, true, role === "owner" ? "makeOwner" : "makeMember"); setMessage("#members-message");
  try {
    const member = await request(projectPath("/members/" + encodeURIComponent(button.dataset.memberId)), { method: "PATCH", body: { role } });
    if (!activeProject(version, projectID)) return;
    await loadMembers();
    const ownerIDs = state.members.filter((item) => item.role === "owner").map((item) => item.id);
    state.project.owner_user_ids = ownerIDs;
    const listedProject = state.projects.find((item) => item.id === projectID);
    if (listedProject) listedProject.owner_user_ids = ownerIDs;
    renderProject(); renderMembers();
    setMessage("#members-message", t("memberRoleUpdated", { username: member.username, role: t(role === "owner" ? "projectOwner" : "projectMember") }), true);
  } catch (error) {
    if (activeProject(version, projectID)) setMessage("#members-message", error.code === "conflict" ? t("lastOwnerRequired") : error.message);
  } finally { if (button.isConnected) setBusy(button, false, role === "owner" ? "makeOwner" : "makeMember"); }
});
$("#add-member-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!state.project) return;
  const form = event.currentTarget, button = form.querySelector("button[type=submit]");
  const identity = $("#member-identity").value.trim(), projectID = state.project.id, version = state.projectVersion;
  const body = identity.includes("@") ? { email: identity } : { username: identity };
  setBusy(button, true, "addMember"); setMessage("#members-message");
  try {
    const member = await request(projectPath("/members"), { method: "POST", body });
    if (!activeProject(version, projectID)) return;
    form.reset(); await loadMembers();
    setMessage("#members-message", t("memberAdded", { username: member.username }), true);
  } catch (error) {
    if (activeProject(version, projectID)) setMessage("#members-message", error.code === "not_found" ? t("memberNotFound") : error.message);
  } finally { setBusy(button, false, "addMember"); }
});
$("#plugins-refresh").addEventListener("click", loadPlugins);
$("#edit-project-timezone").addEventListener("click", () => {
  populateTimezones($("#project-timezone-select"), state.project && state.project.timezone || "UTC");
  setMessage("#project-timezone-message");
  $("#project-timezone-form").classList.remove("hidden");
  $("#edit-project-timezone").classList.add("hidden");
  $("#project-timezone-select").focus();
});
$("#cancel-project-timezone").addEventListener("click", () => {
  $("#project-timezone-form").classList.add("hidden");
  $("#edit-project-timezone").classList.remove("hidden");
  setMessage("#project-timezone-message");
});
$("#project-timezone-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!state.project) return;
  const form = event.currentTarget, button = form.querySelector("button[type=submit]"), projectID = state.project.id, version = state.projectVersion;
  setBusy(button, true, "saveTimezone"); setMessage("#project-timezone-message");
  try {
    const updated = await request(projectPath(""), { method: "PATCH", body: { timezone: $("#project-timezone-select").value } });
    if (!activeProject(version, projectID)) return;
    state.project = updated;
    state.projects = state.projects.map((project) => project.id === updated.id ? updated : project);
    renderProjects(); renderProject(); setMessage("#project-timezone-message", t("timezoneSaved"), true);
  } catch (error) { setMessage("#project-timezone-message", error.message); }
  finally { setBusy(button, false, "saveTimezone"); }
});
$("#plugin-install-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget, button = $("#plugin-install-submit");
  let manifest, config;
  try {
    manifest = jsonLiteral($("#plugin-manifest").value, "pluginManifest", false);
    config = jsonLiteral($("#plugin-config").value || "{}", "pluginConfig", false);
  } catch (error) { setMessage("#plugins-message", error.message); return; }
  setBusy(button, true, "installPluginAction"); setMessage("#plugins-message");
  try {
    const installed = await request(projectPath("/plugins"), { method: "POST", rawBody: jsonWithRaw({}, { manifest: manifest.raw, config: config.raw }) });
    state.pluginToken = installed.token || "";
    $("#plugin-token").textContent = state.pluginToken;
    $("#plugin-token-panel").classList.toggle("hidden", !state.pluginToken);
    form.reset(); $("#plugin-config").value = "{}";
    await loadPlugins(); setMessage("#plugins-message", t("pluginInstalled"), true);
  } catch (error) { setMessage("#plugins-message", error.message); }
  finally { setBusy(button, false, "installPluginAction"); }
});
$("#copy-plugin-token").addEventListener("click", async () => {
  try { await navigator.clipboard.writeText(state.pluginToken); setMessage("#plugins-message", t("tokenCopied"), true); }
  catch { setMessage("#plugins-message", t("copyFailed")); }
});
$("#dismiss-plugin-token").addEventListener("click", () => {
  state.pluginToken = ""; $("#plugin-token").textContent = ""; $("#plugin-token-panel").classList.add("hidden");
});
$("#refresh-button").addEventListener("click", () => loadProjects());
$("#project-unavailable-retry").addEventListener("click", () => loadProjects());

window.addEventListener("popstate", restoreRoute);
window.addEventListener("hashchange", restoreRoute);

async function boot() {
  populateTimezones($("#project-timezone"), BROWSER_TIMEZONE);
  setLocale(state.locale, false); setView(state.view); setContentMode(); renderAudioStatus();
  const expectedSession = Boolean(state.token || state.user || returnedFromCentralAuth);
  const epoch = state.sessionEpoch, token = state.token;
  try {
    const user = await window.ContextNavigation.readSession({ api: API, fetcher: window.fetch.bind(window), storage: localStorage, tokenKey: TOKEN_KEY, userKey: USER_KEY });
    if (!activeSession(epoch, token)) return;
    if (!user) {
      clearSession(); updateIdentity();
      if (expectedSession) setMessage("#auth-message", t("sessionExpired"));
      return;
    }
    state.token = localStorage.getItem(TOKEN_KEY);
    state.user = user; localStorage.setItem(USER_KEY, JSON.stringify(user)); updateIdentity(); await loadProjects();
  } catch (error) {
    if (error.status === 401 || error.code === "unauthenticated") {
      clearSession(); updateIdentity();
      if (expectedSession) setMessage("#auth-message", t("sessionExpired"));
    } else setMessage("#auth-message", t("networkError"));
  }
}
boot();
