const API = new URLSearchParams(window.location.search).get("api") || ((window.location.hostname === "127.0.0.1" || window.location.hostname === "localhost") ? "http://127.0.0.1:8401" : "https://context-api.integ.life");
const TOKEN_KEY = "event-context.token";
const USER_KEY = "event-context.user";

const state = { token: localStorage.getItem(TOKEN_KEY), user: JSON.parse(localStorage.getItem(USER_KEY) || "null"), projects: [], project: null, authMode: "login", contentMode: "text", cursor: "", queryMetadata: {}, events: [] };
const $ = (selector, root = document) => root.querySelector(selector);
const status = $("#connection-status");

function setMessage(id, text = "", success = false) { const el = $(id); el.textContent = text; el.classList.toggle("success", success); }
function setBusy(button, busy, text) { button.disabled = busy; if (text) button.textContent = busy ? "处理中…" : text; }
function jsonObject(value, label) { try { const parsed = JSON.parse(value || "{}"); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error(); return parsed; } catch { throw new Error(`${label} 必须是 JSON object。`); } }
function apiError(payload, statusCode) { return new Error(payload?.error?.message || `请求失败（HTTP ${statusCode}）`); }
async function request(path, { method = "GET", body, auth = true } = {}) {
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (auth && state.token) headers.Authorization = `Bearer ${state.token}`;
  const response = await fetch(`${API}${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const payload = response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) throw apiError(payload, response.status);
  return payload;
}
function saveSession(login) { state.token = login.token; state.user = login.user; localStorage.setItem(TOKEN_KEY, login.token); localStorage.setItem(USER_KEY, JSON.stringify(login.user)); }
function clearSession() { state.token = null; state.user = null; state.projects = []; state.project = null; localStorage.removeItem(TOKEN_KEY); localStorage.removeItem(USER_KEY); }
function updateIdentity() { const online = Boolean(state.user); status.textContent = online ? `已登录：${state.user.username}` : "未登录"; status.classList.toggle("online", online); $("#logout-button").classList.toggle("hidden", !online); $("#auth-panel").classList.toggle("hidden", online); $("#workspace").classList.toggle("hidden", !online); }

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
    const description = document.createElement("small"); description.textContent = project.description || "没有说明";
    button.append(name, description); button.addEventListener("click", () => selectProject(project.id)); list.append(button);
  }
}
async function selectProject(id) {
  state.project = state.projects.find((project) => project.id === id) || null;
  state.cursor = ""; state.events = []; state.queryMetadata = {};
  $("#query-metadata").value = ""; renderProjects(); renderProject();
  if (!state.project) return;
  await Promise.all([loadEvents(), loadMetadata()]);
}
function renderProject() {
  const open = Boolean(state.project); $("#empty-project").classList.toggle("hidden", open); $("#project-view").classList.toggle("hidden", !open);
  if (!open) return;
  $("#project-title").textContent = state.project.name;
  $("#project-description").textContent = state.project.description || "没有项目说明。";
  $("#project-owner").textContent = `OWNER · ${state.project.owner_user_id}`;
}
async function loadEvents({ more = false } = {}) {
  if (!state.project) return;
  const events = await request("/v1/events/query", { method: "POST", body: { project_id: state.project.id, metadata: state.queryMetadata, limit: 30, cursor: more ? state.cursor : "" } });
  state.events = more ? [...state.events, ...events.events] : events.events;
  state.cursor = events.next_cursor || ""; renderEvents();
}
function renderEvents() {
  const list = $("#events-list"); list.replaceChildren();
  if (!state.events.length) { const empty = document.createElement("p"); empty.className = "empty-events"; empty.textContent = "还没有符合条件的 event。"; list.append(empty); }
  const template = $("#event-template");
  for (const event of state.events) {
    const node = template.content.firstElementChild.cloneNode(true);
    $(".event-author", node).textContent = event.actor_username;
    const time = $(".event-time", node); time.dateTime = event.recorded_at; time.textContent = new Date(event.recorded_at).toLocaleString();
    $(".event-kind", node).textContent = event.content.kind;
    if (event.content.kind === "text") $(".event-text", node).textContent = event.content.text;
    else { $(".event-text", node).classList.add("hidden"); const file = $(".event-file", node); file.classList.remove("hidden"); file.textContent = `下载 ${event.content.file.filename} · ${event.content.file.media_type}`; file.addEventListener("click", (click) => downloadFile(click, event.content.file)); }
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
async function loadMetadata() {
  if (!state.project) return;
  const payload = await request("/v1/metadata/query", { method: "POST", body: { project_id: state.project.id, limit: 100 } });
  const list = $("#metadata-list"); list.replaceChildren();
  const fields = payload.fields || [];
  if (!fields.length) { const message = document.createElement("p"); message.className = "muted"; message.textContent = "尚未写入 metadata。"; list.append(message); return; }
  for (const field of fields) { const item = document.createElement("span"); item.className = "metadata-item"; const key = document.createElement("strong"); key.textContent = field.key; item.append(key, document.createTextNode(` · ${field.types.join("/")} · ${field.event_count}`)); list.append(item); }
}

$("#auth-form").addEventListener("submit", async (event) => {
  event.preventDefault(); const submit = $("#auth-submit"); setMessage("#auth-message"); setBusy(submit, true);
  try {
    const credentials = { username: $("#auth-username").value.trim(), password: $("#auth-password").value };
    if (state.authMode === "register") { await request("/v1/auth/register", { method: "POST", body: credentials, auth: false }); setMessage("#auth-message", "注册成功，请登录。", true); state.authMode = "login"; setAuthMode(); }
    else { saveSession(await request("/v1/auth/login", { method: "POST", body: credentials, auth: false })); updateIdentity(); await loadProjects(); }
  } catch (error) { setMessage("#auth-message", error.message); } finally { setBusy(submit, false, state.authMode === "login" ? "登录" : "注册"); }
});
function setAuthMode() { document.querySelectorAll("[data-auth-mode]").forEach((button) => button.classList.toggle("selected", button.dataset.authMode === state.authMode)); $("#auth-submit").textContent = state.authMode === "login" ? "登录" : "注册"; $("#auth-password").autocomplete = state.authMode === "login" ? "current-password" : "new-password"; setMessage("#auth-message"); }
document.querySelectorAll("[data-auth-mode]").forEach((button) => button.addEventListener("click", () => { state.authMode = button.dataset.authMode; setAuthMode(); }));
$("#logout-button").addEventListener("click", async () => { try { await request("/v1/auth/logout", { method: "POST", body: {} }); } finally { clearSession(); updateIdentity(); } });
function showProjectForm(show) { $("#project-form").classList.toggle("hidden", !show); if (show) $("#project-name").focus(); }
$("#new-project-button").addEventListener("click", () => showProjectForm(true)); document.querySelector("[data-open-project-form]").addEventListener("click", () => showProjectForm(true)); $("#cancel-project").addEventListener("click", () => showProjectForm(false));
$("#project-form").addEventListener("submit", async (event) => { event.preventDefault(); const form = event.currentTarget; const button = form.querySelector("button[type=submit]"); setBusy(button, true); try { const project = await request("/v1/projects", { method: "POST", body: { name: $("#project-name").value.trim(), description: $("#project-description").value.trim() } }); form.reset(); showProjectForm(false); await loadProjects(project.id); } catch (error) { alert(error.message); } finally { setBusy(button, false, "创建"); } });
function setContentMode() { document.querySelectorAll("[data-content-mode]").forEach((button) => button.classList.toggle("selected", button.dataset.contentMode === state.contentMode)); $("#text-input-label").classList.toggle("hidden", state.contentMode !== "text"); $("#file-input-label").classList.toggle("hidden", state.contentMode !== "file"); $("#file-type-label").classList.toggle("hidden", state.contentMode !== "file"); }
document.querySelectorAll("[data-content-mode]").forEach((button) => button.addEventListener("click", () => { state.contentMode = button.dataset.contentMode; setContentMode(); }));
$("#record-form").addEventListener("submit", async (event) => { event.preventDefault(); const button = event.currentTarget.querySelector("button[type=submit]"); setMessage("#record-message"); setBusy(button, true); try { const metadata = jsonObject($("#event-metadata").value, "Metadata"); let content; if (state.contentMode === "text") { const text = $("#event-text").value; if (!text) throw new Error("请输入内容。"); content = { kind: "text", text }; } else { const input = $("#event-file"); const file = input.files[0]; const mediaType = $("#event-file-type").value.trim(); if (!file || !mediaType) throw new Error("请选择文件并声明类型。"); const bytes = new Uint8Array(await file.arrayBuffer()); if (bytes.length > 1048576) throw new Error("文件最多 1 MiB。"); content = { kind: "file", file: { filename: file.name, media_type: mediaType, data_base64: btoa(String.fromCharCode(...bytes)) } }; }
    const localTime = $("#event-occurred-at").value; const payload = { project_id: state.project.id, content, metadata, idempotency_key: crypto.randomUUID() }; if (localTime) payload.occurred_at = new Date(localTime).toISOString(); await request("/v1/events", { method: "POST", body: payload }); $("#event-text").value = ""; $("#event-file").value = ""; $("#event-metadata").value = "{}"; $("#event-occurred-at").value = ""; setMessage("#record-message", "已追加，记录不可修改。", true); await Promise.all([loadEvents(), loadMetadata()]);
  } catch (error) { setMessage("#record-message", error.message); } finally { setBusy(button, false, "追加 event"); } });
$("#query-form").addEventListener("submit", async (event) => { event.preventDefault(); try { state.queryMetadata = jsonObject($("#query-metadata").value, "筛选条件"); await loadEvents(); } catch (error) { alert(error.message); } });
$("#clear-query").addEventListener("click", async () => { $("#query-metadata").value = ""; state.queryMetadata = {}; await loadEvents(); }); $("#load-more").addEventListener("click", () => loadEvents({ more: true })); $("#refresh-button").addEventListener("click", () => Promise.all([loadProjects(state.project?.id), loadEvents(), loadMetadata()])); $("#metadata-refresh").addEventListener("click", loadMetadata);

async function boot() { updateIdentity(); setAuthMode(); setContentMode(); if (!state.token) return; try { state.user = await request("/v1/me"); localStorage.setItem(USER_KEY, JSON.stringify(state.user)); updateIdentity(); await loadProjects(); } catch { clearSession(); updateIdentity(); setMessage("#auth-message", "登录已过期，请重新登录。"); } }
boot();
