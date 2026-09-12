const { resolveAPI, sessionStorageKey } = window.ContextWorkspaceUtils;
const API = resolveAPI(location.hostname, new URLSearchParams(location.search).get("api"));
const TOKEN_KEY = sessionStorageKey("event-context.token", API);
const $ = (selector, root = document) => root.querySelector(selector);
const state = { user: null, overview: null, loading: false };

function localToken() {
  try { return localStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; }
}

function errorFrom(payload, status) {
  const error = new Error(payload?.error?.message || `请求失败（${status}）`);
  error.status = status;
  error.code = payload?.error?.code || "";
  return error;
}

async function request(path, options = {}) {
  const headers = {};
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  // Admin endpoints ignore bearer credentials by design; this token keeps /me
  // and local development sessions compatible with the existing workspace.
  if (!path.startsWith("/v1/admin/") && localToken()) headers.Authorization = `Bearer ${localToken()}`;
  let response;
  try {
    response = await fetch(API + path, { method: options.method || "GET", credentials: "include", headers, body: options.body === undefined ? undefined : JSON.stringify(options.body) });
  } catch {
    throw new Error("无法连接 Context API。");
  }
  if (!response.ok) throw errorFrom(await response.json().catch(() => null), response.status);
  return response.status === 204 ? null : response.json();
}

function formatNumber(value) { return new Intl.NumberFormat("zh-CN").format(value || 0); }
function formatBytes(value) {
  let size = Number(value || 0);
  const units = ["B", "KB", "MB", "GB"];
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1; }
  return `${size > 0 ? size.toFixed(size >= 10 || unit === 0 ? 0 : 1) : 0} ${units[unit]}`;
}
function formatDate(value) {
  if (!value) return "暂无活动";
  try { return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
  catch { return value; }
}
function shortID(value) { return value && value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value || "—"; }
function userLabel(user) { return user.email || `@${user.username}`; }
function setMessage(text, success = false) {
  $("#page-message").textContent = text || "";
  $("#page-message").classList.toggle("success", success);
}
function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function showMode(mode) {
  $("#auth-panel").classList.toggle("hidden", mode !== "auth");
  $("#denied-panel").classList.toggle("hidden", mode !== "denied");
  $("#dashboard").classList.toggle("hidden", mode !== "dashboard");
  $("#refresh-button").classList.toggle("hidden", mode !== "dashboard");
  $("#logout-button").classList.toggle("hidden", !state.user);
}

function renderSummary() {
  const totals = state.overview.totals;
  const entries = [
    [totals.users, "注册用户"], [totals.projects, "项目"], [totals.shared_memberships, "分享关系"],
    [totals.events, "记录"], [totals.plugins, "已安装插件"], [formatBytes(totals.file_bytes), `${formatNumber(totals.files)} 个文件`]
  ];
  const root = $("#summary-grid"); root.replaceChildren();
  for (const [value, label] of entries) {
    const card = element("article", "summary-card");
    card.append(element("strong", "", typeof value === "number" ? formatNumber(value) : value), element("span", "", label));
    root.append(card);
  }
}

function renderUsers() {
  const root = $("#user-list"); root.replaceChildren();
  for (const user of state.overview.users) {
    const row = document.createElement("tr");
    const identity = document.createElement("td");
    identity.append(element("strong", "", `@${user.username}`), element("br"), element("code", "", shortID(user.id)));
    row.append(identity, element("td", "", user.email || "—"), element("td", "", formatDate(user.created_at)), element("td", "", formatNumber(user.project_count)), element("td", "", formatNumber(user.owned_project_count)));
    root.append(row);
  }
}

function statPill(text) { return element("span", "stat-pill", text); }
function projectSearchText(project) {
  return [project.name, project.id, ...project.members.flatMap((member) => [member.username, member.email])].join(" ").toLocaleLowerCase();
}
function renderProjects() {
  const root = $("#project-list"); root.replaceChildren();
  if (!state.overview.projects.length) { root.append(element("p", "admin-empty", "还没有项目。")); return; }
  const allUsers = state.overview.users;
  for (const project of state.overview.projects) {
    const card = element("article", "admin-project"); card.dataset.search = projectSearchText(project);
    const head = element("div", "admin-project-head");
    const title = element("div"); title.append(element("h3", "", project.name), element("span", "project-id", project.id), element("p", "muted", project.description || "没有项目描述"));
    head.append(title, element("span", "activity", `最近活动：${formatDate(project.stats.last_activity_at)}`));
    const stats = element("div", "project-stats");
    stats.append(statPill(`${project.members.length} 位成员`), statPill(`${project.stats.event_count} 条记录`), statPill(`${project.stats.file_count} 个文件 · ${formatBytes(project.stats.file_bytes)}`), statPill(`${project.stats.state_count} 个 State`), statPill(`${project.stats.plugin_count} 个插件`), statPill(`Sequence ${project.stats.latest_sequence}`));

    const access = element("div", "access-layout");
    const members = element("div"); members.append(element("h4", "", "访问权限"));
    const memberList = element("div", "member-list");
    for (const member of project.members) {
      const row = element("div", "member-row");
      const identity = element("div", "member-identity"); identity.append(element("strong", "", userLabel(member)), element("small", "", `@${member.username} · ${shortID(member.id)}`));
      const role = member.role === "owner" ? "owner" : "member";
      const roleSelect = document.createElement("select"); roleSelect.className = "role-control"; roleSelect.setAttribute("aria-label", `设置 ${userLabel(member)} 在 ${project.name} 的角色`);
      for (const [value, label] of [["owner", "Owner"], ["member", "成员"]]) { const option = element("option", "", label); option.value = value; roleSelect.append(option); }
      roleSelect.value = role; roleSelect.addEventListener("change", () => changeAccess(project, member, roleSelect.value, roleSelect, role));
      row.append(identity, roleSelect);
      if (role !== "owner") {
        const remove = element("button", "quiet", "移除权限"); remove.type = "button";
        remove.addEventListener("click", () => changeAccess(project, member, "none", remove, role)); row.append(remove);
      }
      memberList.append(row);
    }
    members.append(memberList);
    if (project.stats.plugins.length) {
      const chips = element("div", "plugin-chips");
      for (const plugin of project.stats.plugins) chips.append(element("span", "plugin-chip", `${plugin.name || plugin.plugin_id} · ${plugin.status}`));
      members.append(chips);
    }

    const current = new Set(project.members.map((member) => member.id));
    const available = allUsers.filter((user) => !current.has(user.id));
    const grant = element("form", "grant-form");
    const label = element("label"); label.append(document.createTextNode("添加已有用户"));
    const select = document.createElement("select"); select.setAttribute("aria-label", `为 ${project.name} 添加用户`);
    if (!available.length) {
      const option = element("option", "", "所有用户已有权限"); option.value = ""; select.append(option); select.disabled = true;
    } else {
      for (const user of available) { const option = element("option", "", `${userLabel(user)} · @${user.username}`); option.value = user.id; select.append(option); }
    }
    label.append(select);
    const button = element("button", "primary", "授予成员权限"); button.type = "submit"; button.disabled = !available.length;
    grant.append(label, button);
    grant.addEventListener("submit", (event) => { event.preventDefault(); const user = allUsers.find((item) => item.id === select.value); if (user) changeAccess(project, user, "member", button); });
    access.append(members, grant); card.append(head, stats, access); root.append(card);
  }
  applyFilter();
}

function permissionText(permissions) {
  const entries = [["读", permissions.read_events], ["写", permissions.write_events], ["State", permissions.write_state]];
  return entries.map(([label, values]) => `${label}: ${(values || []).join(", ") || "无"}`).join(" · ");
}
function renderPlugins() {
  const root = $("#plugin-list"); root.replaceChildren();
  if (!state.overview.plugins.length) { root.append(element("p", "admin-empty", "当前没有已安装插件。")); return; }
  const projectMap = new Map(state.overview.projects.map((project) => [project.id, project]));
  const userMap = new Map(state.overview.users.map((user) => [user.id, user]));
  for (const plugin of state.overview.plugins) {
    const card = element("article", "plugin-admin-card");
    card.append(element("h3", "", plugin.name || plugin.plugin_id));
    const meta = element("div", "plugin-meta");
    meta.append(element("span", `status-badge ${plugin.status}`, plugin.status), statPill(`v${plugin.version}`), statPill(projectMap.get(plugin.project_id)?.name || shortID(plugin.project_id)));
    card.append(meta, element("p", "permission-list", permissionText(plugin.permissions)), element("p", "muted", `管理者：${userLabel(userMap.get(plugin.manager_user_id) || { username: shortID(plugin.manager_user_id) })} · 更新：${formatDate(plugin.updated_at)}`));
    root.append(card);
  }
}

function renderDashboard() { renderSummary(); renderProjects(); renderUsers(); renderPlugins(); }
function applyFilter() {
  const query = $("#project-filter").value.trim().toLocaleLowerCase();
  document.querySelectorAll(".admin-project").forEach((card) => card.classList.toggle("filtered", Boolean(query) && !card.dataset.search.includes(query)));
}

async function changeAccess(project, user, access, control, previousAccess = "none") {
  if (access === "none" && !confirm(`移除 ${userLabel(user)} 对“${project.name}”的访问权限？`)) return;
  const isButton = control.tagName === "BUTTON", idle = control.textContent;
  control.disabled = true; if (isButton) control.textContent = "处理中…"; setMessage("");
  try {
    await request(`/v1/admin/projects/${encodeURIComponent(project.id)}/members/${encodeURIComponent(user.id)}`, { method: "PATCH", body: { access } });
    await loadOverview();
    const action = access === "owner" ? "已设为 Owner" : access === "member" ? "已设为成员" : "已移除项目权限";
    setMessage(`${userLabel(user)} ${action}。`, true);
  } catch (error) { setMessage(error.message); control.disabled = false; if (isButton) control.textContent = idle; else control.value = previousAccess; }
}

async function loadOverview() {
  if (state.loading) return;
  state.loading = true; $("#refresh-button").disabled = true;
  try {
    state.overview = await request("/v1/admin/overview");
    showMode("dashboard"); renderDashboard();
  } finally { state.loading = false; $("#refresh-button").disabled = false; }
}

function startLogin() {
  const returnURL = new URL(location.href); returnURL.searchParams.delete("auth");
  const start = new URL(API + "/v1/auth/integ/start");
  start.searchParams.set("ui_locales", "zh-CN"); start.searchParams.set("return_to", returnURL.pathname + returnURL.search);
  location.assign(start.toString());
}

$("#login-button").addEventListener("click", startLogin);
$("#refresh-button").addEventListener("click", () => loadOverview().catch((error) => setMessage(error.message)));
$("#project-filter").addEventListener("input", applyFilter);
$("#logout-button").addEventListener("click", async () => {
  try { await request("/v1/auth/logout", { method: "POST" }); } catch { /* Clear the UI even if the session already expired. */ }
  state.user = null; state.overview = null; $("#session-status").textContent = "已退出"; showMode("auth");
});

async function boot() {
  const clean = new URL(location.href);
  if (clean.searchParams.has("auth")) { clean.searchParams.delete("auth"); history.replaceState(null, "", clean.pathname + clean.search); }
  try {
    state.user = await request("/v1/me");
    $("#session-status").textContent = `已登录：${userLabel(state.user)}`; $("#session-status").classList.add("online");
    await loadOverview();
  } catch (error) {
    if (error.status === 401) { $("#session-status").textContent = "未登录"; showMode("auth"); }
    else if (error.status === 403) { $("#session-status").textContent = `已登录：${userLabel(state.user || {})}`; showMode("denied"); }
    else { $("#session-status").textContent = "服务暂不可用"; showMode(state.user ? "denied" : "auth"); $("#auth-message").textContent = error.message; }
  }
}
boot();
