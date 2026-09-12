(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.ContextWorkspacePluginConfig = api;
})(typeof globalThis === "undefined" ? this : globalThis, function () {
  function configurationFor(plugin) {
    const rawConfig = plugin && plugin.config;
    const config = rawConfig && typeof rawConfig === "object" && !Array.isArray(rawConfig) ? rawConfig : {};
    const fields = Array.isArray(plugin && plugin.config_fields)
      ? plugin.config_fields.filter((field) => field && typeof field.key === "string" && field.key)
      : [];
    return { config, fields, json: JSON.stringify(config, null, 2) };
  }

  function defaultFor(config, key) {
    if (!Object.prototype.hasOwnProperty.call(config, key)) return null;
    return JSON.stringify(config[key]);
  }

  return { configurationFor, defaultFor };
});
