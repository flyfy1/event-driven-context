(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.ContextWorkspaceUtils = api;
})(typeof globalThis === "undefined" ? this : globalThis, function () {
  const PRODUCTION_API = "https://context-api.integ.life";
  const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost", "::1"]);
  const AUDIO_TYPES = new Set(["audio/mp4", "audio/wav", "audio/mpeg"]);
  const IMAGE_TYPES = new Set(["image/jpeg", "image/png"]);
  const IMAGE_EXTENSIONS = new Set(["jpeg", "jpg", "png"]);
  const REFERENCE_RELATIONS = new Set(["supersedes", "retracts", "resolves", "derived_from", "replies_to"]);

  function resolveAPI(pageHostname, requestedAPI) {
    if (!LOOPBACK_HOSTS.has(pageHostname)) return PRODUCTION_API;
    if (!requestedAPI) return "http://127.0.0.1:8401";
    try {
      const candidate = new URL(requestedAPI);
      if (!["http:", "https:"].includes(candidate.protocol) || !LOOPBACK_HOSTS.has(candidate.hostname)) return "http://127.0.0.1:8401";
      return candidate.origin;
    } catch {
      return "http://127.0.0.1:8401";
    }
  }

  function sessionStorageKey(base, api) {
    return api === PRODUCTION_API ? base : `${base}.${encodeURIComponent(api)}`;
  }

  function bytesToBase64(bytes) {
    const chunks = [];
    for (let offset = 0; offset < bytes.length; offset += 0x8000) chunks.push(String.fromCharCode(...bytes.subarray(offset, offset + 0x8000)));
    return btoa(chunks.join(""));
  }

  function audioMediaType(filename, declaredType) {
    const declared = String(declaredType || "").toLowerCase();
    if (declared === "audio/x-wav") return "audio/wav";
    if (declared === "audio/mp3") return "audio/mpeg";
    if (AUDIO_TYPES.has(declared)) return declared;
    const extension = String(filename || "").toLowerCase().split(".").pop();
    return ({ m4a: "audio/mp4", mp4: "audio/mp4", wav: "audio/wav", mp3: "audio/mpeg" })[extension] || "";
  }

  function contextFileKind(filename, declaredType) {
    const declared = String(declaredType || "").toLowerCase();
    if (audioMediaType(filename, declared)) return "audio";
    if (IMAGE_TYPES.has(declared)) return "image";
    const extension = String(filename || "").toLowerCase().split(".").pop();
    return IMAGE_EXTENSIONS.has(extension) ? "image" : "";
  }

  function fileTitle(filename) {
    const name = String(filename || "").trim().replace(/^.*[\\/]/, "");
    const dot = name.lastIndexOf(".");
    const stem = dot > 0 ? name.slice(0, dot) : name;
    return stem.replace(/[_-]+/g, " ").replace(/\s+/g, " ").trim();
  }

  function buildMetadata(title, description, tagText, customFields) {
    const metadata = {};
    const normalizedTitle = String(title || "").trim();
    const normalizedDescription = String(description || "").trim();
    const tags = Array.from(new Set(String(tagText || "").split(/[,\n]/).map((tag) => tag.trim()).filter(Boolean)));
    if (normalizedTitle) metadata.title = normalizedTitle;
    if (normalizedDescription) metadata.description = normalizedDescription;
    if (tags.length) metadata.tags = tags;
    const reserved = new Set(Object.keys(metadata).concat(["title", "description", "tags"]));
    for (const field of customFields || []) {
      const key = String(field && field.key || "").trim();
      const value = String(field && field.value || "").trim();
      if (!key && !value) continue;
      if (!key) throw new Error("metadata_key_required");
      if (reserved.has(key)) throw new Error("metadata_key_duplicate");
      reserved.add(key);
      metadata[key] = value;
    }
    return metadata;
  }

  function buildMetadataFilter(key, value) {
    const normalizedKey = String(key || "").trim();
    const normalizedValue = String(value || "").trim();
    if (!normalizedKey && !normalizedValue) return {};
    if (!normalizedKey) throw new Error("metadata_key_required");
    return { [normalizedKey]: normalizedValue };
  }

  function buildReferences(fields) {
    const references = [];
    const seen = new Set();
    for (const field of fields || []) {
      const rel = String(field && field.rel || "").trim();
      const id = String(field && field.id || "").trim().toLowerCase();
      if (!rel && !id) continue;
      if (!rel || !id) throw new Error("reference_fields_required");
      if (!REFERENCE_RELATIONS.has(rel)) throw new Error("reference_relation_invalid");
      const key = rel + "\x00" + id;
      if (seen.has(key)) throw new Error("reference_duplicate");
      seen.add(key);
      references.push({ rel, id });
    }
    return references;
  }

  function localDateTimeValue(date = new Date()) {
    const pad = (value) => String(value).padStart(2, "0");
    return [date.getFullYear(), pad(date.getMonth() + 1), pad(date.getDate())].join("-") + "T" + [pad(date.getHours()), pad(date.getMinutes())].join(":");
  }

  function pathWithLocale(href, locale) {
    const target = new URL(href);
    target.searchParams.set("locale", locale);
    return `${target.pathname}${target.search}${target.hash}`;
  }

  function uuidV7(now = Date.now(), random) {
    const bytes = new Uint8Array(16);
    if (random) bytes.set(random.slice(0, 16));
    else globalThis.crypto.getRandomValues(bytes);
    let timestamp = BigInt(now);
    for (let index = 5; index >= 0; index -= 1) {
      bytes[index] = Number(timestamp & 255n);
      timestamp >>= 8n;
    }
    bytes[6] = (bytes[6] & 15) | 112;
    bytes[8] = (bytes[8] & 63) | 128;
    const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
    return hex.slice(0, 8) + "-" + hex.slice(8, 12) + "-" + hex.slice(12, 16) + "-" + hex.slice(16, 20) + "-" + hex.slice(20);
  }

  return { PRODUCTION_API, resolveAPI, sessionStorageKey, bytesToBase64, audioMediaType, contextFileKind, fileTitle, buildMetadata, buildMetadataFilter, buildReferences, localDateTimeValue, pathWithLocale, uuidV7 };
});
