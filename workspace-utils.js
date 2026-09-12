(function (root, factory) {
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.ContextWorkspaceUtils = api;
})(typeof globalThis === "undefined" ? this : globalThis, function () {
  const PRODUCTION_API = "https://context-api.integ.life";
  const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost", "::1"]);
  const AUDIO_TYPES = new Set(["audio/mp4", "audio/wav", "audio/mpeg"]);

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

  return { PRODUCTION_API, resolveAPI, sessionStorageKey, bytesToBase64, audioMediaType, pathWithLocale, uuidV7 };
});
