(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.ContextNavigation = api;
})(typeof globalThis === "undefined" ? this : globalThis, function () {
  const views = new Set(["records", "files", "state", "integration", "plugins"]);
  function workspaceURL(href, locale) {
    const source = new URL(href);
    const target = new URL("./workspace.html", source);
    for (const key of ["api", "project", "auth"]) {
      if (source.searchParams.has(key)) target.searchParams.set(key, source.searchParams.get(key));
    }
    if (locale) target.searchParams.set("locale", locale);
    const view = source.searchParams.get("view") || source.hash.slice(1);
    if (views.has(view)) target.hash = view;
    return target.pathname + target.search + target.hash;
  }
  function homeURL(href, locale, about = false) {
    const source = new URL(href);
    const target = new URL("./", source);
    if (source.searchParams.has("api")) target.searchParams.set("api", source.searchParams.get("api"));
    if (locale) target.searchParams.set("locale", locale);
    if (about) {
      target.searchParams.set("page", "about");
      if (source.searchParams.has("project")) target.searchParams.set("project", source.searchParams.get("project"));
      const view = source.hash.slice(1);
      if (views.has(view)) target.searchParams.set("view", view);
    }
    return target.pathname + target.search;
  }
  function workspaceIntent(href) {
    const url = new URL(href);
    return url.searchParams.get("auth") === "complete" || (url.searchParams.get("page") !== "about" && (url.searchParams.has("project") || views.has(url.hash.slice(1))));
  }
  // Verify the HttpOnly central session first; a stale legacy token must not
  // override a newly signed-in central identity. Network errors are not logout.
  async function readSession({ api, fetcher, storage, tokenKey, userKey, signal }) {
    const request = (token) => fetcher(api + "/v1/me", {
      credentials: "include", signal, headers: token ? { Authorization: "Bearer " + token } : {}
    });
    let response = await request();
    const cookieAuthenticated = response.ok;
    if (response.status === 401) {
      let token;
      try { token = storage.getItem(tokenKey); } catch { /* Cookie auth still works. */ }
      if (token) response = await request(token);
    }
    if (response.status === 401) {
      try { storage.removeItem(tokenKey); storage.removeItem(userKey); } catch { /* Storage may be disabled. */ }
      return null;
    }
    if (!response.ok) throw new Error("session_unavailable");
    const user = await response.json();
    if (!user || !user.id) throw new Error("invalid_session_response");
    if (cookieAuthenticated) {
      try { storage.removeItem(tokenKey); storage.removeItem(userKey); } catch { /* Cookie remains authoritative. */ }
    }
    return user;
  }
  return { workspaceURL, homeURL, workspaceIntent, readSession };
});
