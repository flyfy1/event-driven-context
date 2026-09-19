(function () {
  "use strict";
  const page = Boolean(document.querySelector("#hub-main"));
  const reminder = document.querySelector("#hub-authorizations-link");
  if (!page && !reminder) return;
  const { resolveAPI, sessionStorageKey } = window.ContextWorkspaceUtils;
  const params = new URLSearchParams(location.search);
  const api = resolveAPI(location.hostname, params.get("api"));
  const tokenKey = sessionStorageKey("event-context.token", api);
  const userKey = sessionStorageKey("event-context.user", api);
  const words = {
    en: {
      googleTitle: "Connect a Google account", googleHint: "Choose a source, then sign in and review permissions at Google. Repeat to connect several accounts.", connectionName: "Connection name", connectionPlaceholder: "Work account", googleConnect: "Continue to Google", googleReady: "You will choose the Google account on Google's authorization page.", googleChecking: "Checking whether Google connections are enabled…", googleUnconfigured: "Google connections are not configured on this server. The deployer needs to configure Google OAuth and credential storage.", googleUnavailable: "Cannot check Google connection availability. Try Refresh.", googleStarting: "Opening Google authorization…", googleFailed: "Could not start Google authorization. Try again.", googleInvalidURL: "The server returned an unexpected authorization address. Navigation was blocked.", googleNameRequired: "Enter a connection name and choose a supported Google source.", googleDriveScope: "Google Drive: this connection requests read-only access to all Drive files your account can access. Agent API access requires separate approval.", googleMailScope: "Gmail: this connection requests read-only access to your mailbox. Agent API access requires separate approval.",
      language: "Language", eyebrow: "CONTEXT HUB", title: "My authorizations", intro: "Control which agents can use each connected account and API operation.", refresh: "Refresh",
      signInTitle: "Sign in to review requests", signInBody: "Use your owner account to manage agents and data access.", signIn: "Open workspace to sign in",
      polling: "Updates every 30 seconds while this page is open and visible.", agents: "Agents", requests: "API access", connections: "Connected accounts",
      agentHint: "Approve only an agent you are setting up. Its name is self-reported. Confirm the verification code in its CLI before approving. Approval reveals connected account names and identities for 7 days. Reading source content requires a separate authorization.",
      requestHint: "Each authorization covers one account and one API operation until the stated expiry. Read the exact scope before approving.",
      connectionHint: "Each account is a separate connection. Disconnecting an account stops future access through the Hub.",
      noneAgents: "No agents yet. An agent can start registration through the CLI.", noneRequests: "No API access requests yet.", noneConnections: "No connected accounts yet.",
      pending: "Pending", approved: "Approved", denied: "Denied", revoked: "Revoked", expired: "Expired", configured: "Configured · access not verified", connected: "Connected", disconnected: "Disconnected", reauth_required: "Reconnect required", needs_auth: "Reconnect required", blocked: "Blocked", unconfigured: "Not configured",
      approvalDeadline: "Review deadline", agentDuration: "Approving allows this agent to discover connected account names and identities for 7 days. Reading source content still requires a separate API authorization.", agentId: "Agent ID", account: "Account", connection: "Connection", connectionId: "Connection ID", provider: "Provider", operation: "API operation", reason: "Agent-provided reason (untrusted)", created: "Created", expires: "Expires", agent: "Agent", approve: "Approve", deny: "Deny", revoke: "Revoke access", disconnect: "Disconnect", confirmDisconnect: "Disconnect this account? Future API access through this connection will stop.",
      code: "Enter the verification code shown by your agent's CLI", codeHint: "Use the code from the CLI session you initiated, not a code supplied in a message or request reason.", codeMismatch: "The code does not match this registration. Check the CLI before approving.",
      missingConnection: "This connection is unavailable. Approval is disabled.", fullScope: "This grants the operation across all resources that the connected account and provider credential permit. The request reason does not restrict access.",
      unavailable: "Authorizations are unavailable on this server. The Hub API may not be installed yet.", failed: "Unable to load authorizations. Check your connection and try Refresh.", mutationFailed: "The change could not be confirmed. Refresh to check the current status before trying again.", sessionChanged: "Your signed-in account changed. Review the requests again before taking action.", invalidData: "The server returned an invalid authorization response. No approval is available.", forbidden: "Use your owner account to review authorizations. Agent credentials cannot approve access.", loading: "Checking your session…", updated: "Authorization updated.", noExpiry: "No expiry provided", pendingCount: (n) => `${n} pending request${n === 1 ? "" : "s"}`, reminderError: "My authorizations · status unavailable", missingAgent: "The agent is not approved or its registration has expired. Approval is disabled.",
    },
    "zh-CN": {
      googleTitle: "连接 Google 账号", googleHint: "选择数据源，然后前往 Google 登录并确认权限。可重复操作以连接多个账号。", connectionName: "连接名称", connectionPlaceholder: "工作账号", googleConnect: "前往 Google", googleReady: "你将在 Google 授权页面选择要连接的账号。", googleChecking: "正在检查 Google 连接是否已启用…", googleUnconfigured: "此服务器尚未配置 Google 连接，需要部署者配置 Google OAuth 和凭证存储。", googleUnavailable: "无法确认 Google 连接是否可用，请刷新重试。", googleStarting: "正在打开 Google 授权…", googleFailed: "无法开始 Google 授权，请重试。", googleInvalidURL: "服务器返回了非预期的授权地址，已阻止跳转。", googleNameRequired: "请输入连接名称并选择支持的 Google 数据源。", googleDriveScope: "Google Drive：此连接申请只读访问该账号可以访问的所有 Drive 文件。Agent 访问 API 仍需单独批准。", googleMailScope: "Gmail：此连接申请只读访问你的邮箱。Agent 访问 API 仍需单独批准。",
      language: "语言", eyebrow: "CONTEXT HUB", title: "我的授权", intro: "管理每个 Agent 可以使用的账号和 API 操作。", refresh: "刷新",
      signInTitle: "登录后查看授权申请", signInBody: "使用你的所有者账号管理 Agent 和数据访问权限。", signIn: "打开工作区登录",
      polling: "页面打开并可见时，每 30 秒更新一次。", agents: "Agent", requests: "API 访问权限", connections: "已连接账号",
      agentHint: "只批准你正在配置的 Agent。名称由 Agent 自行提供。批准前请确认其 CLI 中的验证码。批准后，Agent 可以在 7 天内查看已连接账号的名称和身份信息。读取数据源内容仍需单独授权。",
      requestHint: "每份授权仅适用于一个账号和一个 API 操作，有效期如下所示。批准前请仔细确认权限范围。",
      connectionHint: "每个账号都是独立的连接。断开后将停止通过 Hub 访问该账号。",
      noneAgents: "暂无 Agent。Agent 可以通过 CLI 发起注册。", noneRequests: "暂无 API 访问申请。", noneConnections: "暂无已连接账号。",
      pending: "待处理", approved: "已批准", denied: "已拒绝", revoked: "已撤销", expired: "已过期", configured: "已配置 · 尚未验证访问", connected: "已连接", disconnected: "已断开", reauth_required: "需要重新连接", needs_auth: "需要重新连接", blocked: "已阻止", unconfigured: "未配置",
      approvalDeadline: "审核截止时间", agentDuration: "批准后，此 Agent 可以在 7 天内查看已连接账号的名称和身份信息。读取数据源内容仍需另外授予 API 权限。", agentId: "Agent ID", account: "账号", connection: "连接", connectionId: "连接 ID", provider: "数据源", operation: "API 操作", reason: "Agent 提供的原因（不可信内容）", created: "创建时间", expires: "到期时间", agent: "Agent", approve: "批准", deny: "拒绝", revoke: "撤销权限", disconnect: "断开连接", confirmDisconnect: "确定断开此账号吗？之后将无法通过此连接访问 API。",
      code: "输入 Agent CLI 中显示的验证码", codeHint: "请使用你亲自发起的 CLI 会话中的验证码，不要使用消息或申请原因中提供的代码。", codeMismatch: "验证码与本次注册不匹配，请先核对 CLI。",
      missingConnection: "此连接不可用，无法批准。", fullScope: "授权涵盖此账号和第三方凭证允许访问的所有资源上的该操作。申请原因不会限制实际访问范围。",
      unavailable: "此服务器尚无法提供授权管理，可能尚未安装 Hub API。", failed: "无法加载授权，请检查网络后刷新。", mutationFailed: "无法确认修改结果，请刷新查看当前状态后再重试。", sessionChanged: "登录账号已更改，请重新查看申请后再操作。", invalidData: "服务器返回了无效的授权数据，无法批准。", forbidden: "请使用所有者账号管理授权。Agent 凭证不能批准访问权限。", loading: "正在检查登录状态…", updated: "授权已更新。", noExpiry: "未提供到期时间", pendingCount: (n) => `${n} 个待处理申请`, reminderError: "我的授权 · 状态暂不可用", missingAgent: "此 Agent 未获批准或注册已过期，无法批准。",
    }
  };
  const saved = (key) => { try { return localStorage.getItem(key); } catch { return null; } };
  let locale = (params.get("locale") || saved("event-context.locale") || navigator.language || "en").startsWith("zh") ? "zh-CN" : "en";
  let user = null, token = null, epoch = 0, requestVersion = 0, snapshot = null, busy = false, fetching = false, timer = null;
  let controller = new AbortController();
  let renderedStatuses = "", googleState = "checking", googleMessage = "";
  const t = (key) => words[locale][key] || key;
  const $ = (selector) => document.querySelector(selector);
  const stamp = () => `${saved(tokenKey) || ""}\u0000${saved(userKey) || ""}`;
  const current = (version, authStamp) => version === epoch && authStamp === stamp();
  const node = (tag, text, className) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if (className) el.className = className; return el; };
  function linkTo(file) { const url = new URL(file, location.href); if (params.has("api")) url.searchParams.set("api", params.get("api")); url.searchParams.set("locale", locale); return url.pathname + url.search; }
  function message(text, success = false) { if (!page) return; $("#hub-message").textContent = text; $("#hub-message").classList.toggle("success", success); }
  function expired(item) { const time = Date.parse(item.expires_at); return !Number.isFinite(time) || time <= Date.now(); }
  function effectiveStatus(item) { return ["pending", "approved"].includes(item.status) && expired(item) ? "expired" : item.status; }
  function formatDate(value) { const date = new Date(value); return !Number.isFinite(date.getTime()) ? t("noExpiry") : `${date.toLocaleString(locale)} (${Intl.DateTimeFormat().resolvedOptions().timeZone})`; }
  function countPending(data) { return [...data.agents, ...data.requests].filter((item) => effectiveStatus(item) === "pending").length; }
  function updateReminder(count, failed = false) {
    if (!reminder) return;
    reminder.href = linkTo("./hub.html");
    reminder.textContent = failed ? t("reminderError") : `${t("title")}${count ? ` · ${count}` : ""}`;
    reminder.setAttribute("aria-label", failed ? t("reminderError") : `${t("title")}, ${t("pendingCount")(count || 0)}`);
    reminder.classList.toggle("hidden", !user);
  }
  function translate() {
    if (page) {
      document.documentElement.lang = locale;
      document.title = `${t("title")} · Event Driven Context`;
      document.querySelectorAll("[data-hub-text]").forEach((el) => { el.textContent = t(el.dataset.hubText); });
      document.querySelectorAll("[data-hub-placeholder]").forEach((el) => { el.placeholder = t(el.dataset.hubPlaceholder); });
      $("#hub-language").value = locale;
      renderGoogle();
      $("#hub-workspace").href = $("#hub-signin-link").href = linkTo("./workspace.html");
      $(".hub-nav").setAttribute("aria-label", t("title"));
      if (snapshot) render(snapshot);
    }
    updateReminder(snapshot ? countPending(snapshot) : 0);
  }
  function reset() {
    epoch += 1; requestVersion += 1; controller.abort(); controller = new AbortController();
    user = null; token = null; snapshot = null; busy = false; fetching = false; googleState = "checking"; googleMessage = ""; clearTimeout(timer);
    if (page) { $("#hub-content").hidden = true; $("#hub-identity").textContent = ""; ["agents", "requests", "connections"].forEach((key) => $("#hub-" + key).replaceChildren()); }
    updateReminder(0);
  }
  async function call(path, options = {}) {
    const headers = token ? { Authorization: "Bearer " + token } : {};
    if (options.body !== undefined) headers["Content-Type"] = "application/json";
    const response = await fetch(api + path, { credentials: "include", signal: controller.signal, ...options, headers, body: options.body === undefined ? undefined : JSON.stringify(options.body) });
    if (!response.ok) { const error = new Error(`HTTP ${response.status}`); error.status = response.status; const payload = await response.json().catch(() => null); error.code = payload?.error?.code; throw error; }
    if (response.status === 204) return null;
    return response.json();
  }
  function loadError(error, mutation = false) {
    if (error.name === "AbortError") return;
    if (error.status === 401) { reset(); if (page) $("#hub-signin").hidden = false; message(t("signInTitle")); return; }
    message(t(error.status === 404 ? "unavailable" : error.status === 403 ? "forbidden" : mutation ? "mutationFailed" : "failed"));
    updateReminder(0, true);
  }
  function schedule() { clearTimeout(timer); if (user && !document.hidden) timer = setTimeout(refresh, 30000); }
  async function checkIdentity(expected) {
    // A fresh server identity check also detects cookie changes that do not emit storage events.
    const identity = await call("/v1/me");
    if (!identity || identity.id !== expected) { reset(); message(t("sessionChanged")); void start(); return false; }
    return true;
  }
  async function refresh() {
    if (!user || document.hidden || busy || fetching) return;
    fetching = true;
    const version = epoch, authStamp = stamp(), requestID = ++requestVersion, identity = user.id;
    try {
      if (!await checkIdentity(identity) || !current(version, authStamp)) return;
      const [data, google] = await Promise.all([
        call("/v1/hub/owner"),
        page ? call("/v1/hub/google/status").then((result) => typeof result?.configured === "boolean" ? result : null).catch(() => null) : null
      ]);
      if (!current(version, authStamp) || requestID !== requestVersion) return;
      if (!data || !["agents", "requests", "connections"].every((key) => Array.isArray(data[key]))) throw new Error("invalid_response");
      const statuses = [...data.agents, ...data.requests].map(effectiveStatus).join("|");
      const changed = JSON.stringify(snapshot) !== JSON.stringify(data) || renderedStatuses !== statuses;
      snapshot = data;
      if (page) { googleMessage = ""; googleState = google === null ? "unavailable" : google.configured ? "ready" : "unconfigured"; renderGoogle(); }
      if (page) { $("#hub-content").hidden = false; $("#hub-signin").hidden = true; message(""); if (changed) render(data); else $("#hub-pending").textContent = t("pendingCount")(countPending(data)); }
      updateReminder(countPending(data));
    } catch (error) { if (current(version, authStamp)) loadError(error); }
    finally { if (version === epoch) { fetching = false; schedule(); } }
  }
  async function start() {
    reset(); translate();
    const version = epoch;
    if (page) message(t("loading"));
    try {
      const identity = await window.ContextNavigation.readSession({ api, fetcher: fetch.bind(window), storage: localStorage, tokenKey, userKey, signal: controller.signal });
      // readSession may remove stale storage itself; the epoch guards account changes during it.
      if (version !== epoch) return;
      if (!identity) { if (page) $("#hub-signin").hidden = false; message(""); return; }
      user = identity; token = saved(tokenKey);
      if (page) { $("#hub-identity").textContent = user.email || user.username || user.id; $("#hub-signin").hidden = true; }
      updateReminder(0);
      await refresh();
    } catch (error) { if (version === epoch) loadError(error); }
  }
  function renderGoogle() {
    if (!page) return;
    $("#hub-google-connect").disabled = googleState !== "ready" || busy || !user;
    $("#hub-google-message").textContent = t(googleMessage || ({ checking: "googleChecking", ready: "googleReady", unconfigured: "googleUnconfigured", unavailable: "googleUnavailable" }[googleState]));
    $("#hub-google-scope").textContent = t($("#hub-google-provider").value === "gmail" ? "googleMailScope" : "googleDriveScope");
  }
  async function connectGoogle(event) {
    event.preventDefault();
    if (busy || !user || googleState !== "ready") return;
    const provider = $("#hub-google-provider").value, name = $("#hub-google-name").value.trim();
    if (!["google-drive", "gmail"].includes(provider) || !name || name.length > 150 || /[\r\n\x00]/.test(name)) { googleMessage = "googleNameRequired"; renderGoogle(); return; }
    busy = true; clearTimeout(timer); requestVersion += 1;
    const version = epoch, authStamp = stamp(), identity = user.id;
    googleMessage = "googleStarting"; renderGoogle();
    try {
      if (!await checkIdentity(identity) || !current(version, authStamp)) return;
      const result = await call("/v1/hub/google/start", { method: "POST", body: { provider_id: provider, display_name: name } });
      if (!current(version, authStamp)) return;
      let destination;
      try { destination = new URL(result?.authorization_url); } catch { /* Reject absent or malformed server URLs. */ }
      if (!destination || destination.origin !== "https://accounts.google.com" || destination.username || destination.password) { googleMessage = "googleInvalidURL"; return; }
      window.location.assign(destination.href);
    } catch (error) {
      if (!current(version, authStamp) || error.name === "AbortError") return;
      if (error.status === 401) { loadError(error); return; }
      googleMessage = error.code === "service_unavailable" || error.status === 503 ? "googleUnconfigured" : "googleFailed";
    } finally { if (version === epoch) { busy = false; renderGoogle(); schedule(); } }
  }
  function details(entries) {
    const dl = node("dl", undefined, "hub-details");
    entries.forEach(([label, value]) => { dl.append(node("dt", t(label)), node("dd", value || "—")); });
    return dl;
  }
  function button(label, action, primary = false) { const b = node("button", t(label), primary ? "primary" : "hub-danger"); b.type = "button"; b.addEventListener("click", action); return b; }
  function card(title, status) {
    const article = node("article", undefined, `hub-card ${status === "pending" ? "pending" : ""}`);
    const header = node("div", undefined, "hub-card-header");
    header.append(node("h3", title), node("span", t(status), "hub-state")); article.append(header); return article;
  }
  function ordered(items) { return [...items].sort((a, b) => Number(effectiveStatus(b) === "pending") - Number(effectiveStatus(a) === "pending") || String(b.created_at || "").localeCompare(String(a.created_at || ""))); }
  function render(data) {
    if (!page) return;
    renderedStatuses = [...data.agents, ...data.requests].map(effectiveStatus).join("|");
    $("#hub-pending").textContent = t("pendingCount")(countPending(data));
    for (const key of ["agents", "requests", "connections"]) $("#hub-" + key).replaceChildren();
    for (const agent of ordered(data.agents)) {
      const status = effectiveStatus(agent), item = card(agent.name, status);
      item.append(details([["agentId", agent.id], ["created", formatDate(agent.created_at)], [status === "pending" ? "approvalDeadline" : "expires", formatDate(agent.expires_at)]]));
      if (status === "pending") {
        item.append(node("p", t("agentDuration"), "hub-note"));
        const label = node("label", t("code"), "hub-code-label"), input = node("input");
        input.type = "text"; input.autocomplete = "off"; input.spellcheck = false; input.maxLength = 128; label.append(input); item.append(label, node("p", t("codeHint"), "muted hub-code-hint"));
        const actions = node("div", undefined, "actions"), approve = button("approve", () => {
          const code = input.value.trim().toUpperCase();
          if (!code || code !== agent.verification_code) { message(t("codeMismatch")); input.focus(); return; }
          void decide("agents", agent.id, "approve", { verification_code: code });
        }, true);
        approve.disabled = !agent.verification_code;
        actions.append(approve, button("deny", () => void decide("agents", agent.id, "deny"))); item.append(actions);
      } else if (status === "approved") { const actions = node("div", undefined, "actions"); actions.append(button("revoke", () => void decide("agents", agent.id, "revoke"))); item.append(actions); }
      $("#hub-agents").append(item);
    }
    for (const request of ordered(data.requests)) {
      const status = effectiveStatus(request), connection = data.connections.find((c) => c.id === request.connection_id), agent = data.agents.find((a) => a.id === request.agent_id);
      const item = card(request.operation, status);
      item.append(details([["agent", request.agent_name], ["agentId", request.agent_id], ["connection", connection?.display_name || request.connection_name], ["connectionId", request.connection_id], ["provider", connection?.provider_id], ["account", connection?.account_id], ["operation", request.operation], ["reason", request.reason], ["created", formatDate(request.created_at)], ["expires", formatDate(request.expires_at)]]));
      if (status === "pending" || status === "approved") item.append(node("p", t("fullScope"), "hub-scope"));
      const actions = node("div", undefined, "actions");
      if (status === "pending") {
        const canConnect = connection && ["configured", "connected"].includes(connection.status), canAgent = agent && effectiveStatus(agent) === "approved";
        const approve = button("approve", () => void decide("requests", request.id, "approve"), true); approve.disabled = !canConnect || !canAgent;
        actions.append(approve, button("deny", () => void decide("requests", request.id, "deny")));
        if (!canConnect) item.append(node("p", t("missingConnection"), "hub-note"));
        if (!canAgent) item.append(node("p", t("missingAgent"), "hub-note"));
      } else if (status === "approved") actions.append(button("revoke", () => void decide("requests", request.id, "revoke")));
      if (actions.childNodes.length) item.append(actions);
      $("#hub-requests").append(item);
    }
    for (const connection of data.connections) {
      const item = card(connection.display_name, connection.status);
      item.append(details([["provider", connection.provider_id], ["account", connection.account_id], ["connectionId", connection.id]]));
      if (connection.status !== "disconnected") { const actions = node("div", undefined, "actions"); actions.append(button("disconnect", () => { if (window.confirm(t("confirmDisconnect"))) void decide("connections", connection.id, "disconnect"); })); item.append(actions); }
      $("#hub-connections").append(item);
    }
    for (const [key, empty] of [["agents", "noneAgents"], ["requests", "noneRequests"], ["connections", "noneConnections"]]) if (!data[key].length) $("#hub-" + key).append(node("p", t(empty), "hub-empty"));
  }
  async function decide(kind, id, decision, extra = {}) {
    if (busy || !user) return;
    busy = true; clearTimeout(timer); requestVersion += 1;
    const version = epoch, authStamp = stamp(), identity = user.id;
    document.querySelectorAll("#hub-content button").forEach((b) => { b.disabled = true; });
    try {
      if (!await checkIdentity(identity) || !current(version, authStamp)) return;
      await call(`/v1/hub/${kind}/${encodeURIComponent(id)}/${decision === "disconnect" ? "disconnect" : "decision"}`, { method: "POST", body: decision === "disconnect" ? {} : { decision, ...extra } });
      if (!current(version, authStamp)) return;
      snapshot = null; busy = false; fetching = false; await refresh();
      if (current(version, authStamp)) message(t("updated"), true);
    } catch (error) { if (current(version, authStamp)) loadError(error, true); }
    finally { if (version === epoch) { busy = false; if (snapshot) render(snapshot); renderGoogle(); schedule(); } }
  }
  window.addEventListener("storage", (event) => { if (event.key === null || event.key === tokenKey || event.key === userKey) void start(); else if (event.key === "event-context.locale") { locale = (event.newValue || "en").startsWith("zh") ? "zh-CN" : "en"; translate(); } });
  document.addEventListener("visibilitychange", () => { if (document.hidden) clearTimeout(timer); else if (user) void refresh(); else void start(); });
  if (page) {
    $("#hub-google-form").addEventListener("submit", connectGoogle);
    $("#hub-google-provider").addEventListener("change", renderGoogle);
    $("#hub-refresh").addEventListener("click", () => { if (user) void refresh(); else void start(); });
    $("#hub-language").addEventListener("change", (event) => { locale = event.target.value; try { localStorage.setItem("event-context.locale", locale); } catch { /* The page remains usable without storage. */ } const url = new URL(location.href); url.searchParams.set("locale", locale); history.replaceState(null, "", url.pathname + url.search); translate(); });
  } else {
    window.addEventListener("context:identity", (event) => {
      locale = String(event.detail.locale || "en").startsWith("zh") ? "zh-CN" : "en";
      if (!event.detail.userId) { reset(); return; }
      if (!user || user.id !== event.detail.userId) void start(); else translate();
    });
  }
  void start();
})();
