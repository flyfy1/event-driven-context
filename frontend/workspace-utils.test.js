const assert = require("node:assert/strict");
const test = require("node:test");
const { PRODUCTION_API, resolveAPI, sessionStorageKey, bytesToBase64, audioMediaType, pathWithLocale, uuidV7 } = require("./workspace-utils.js");

test("production pages ignore API overrides", () => {
  assert.equal(resolveAPI("context.integ.life", "https://example.com"), PRODUCTION_API);
  assert.equal(resolveAPI("context.integ.life", "http://localhost:9999"), PRODUCTION_API);
});

test("development overrides remain on loopback and have separate session keys", () => {
  assert.equal(resolveAPI("localhost", "http://localhost:9000/path"), "http://localhost:9000");
  assert.equal(resolveAPI("localhost", "https://example.com"), "http://127.0.0.1:8401");
  assert.notEqual(sessionStorageKey("event-context.token", "http://localhost:9000"), sessionStorageKey("event-context.token", "http://127.0.0.1:8401"));
  assert.equal(sessionStorageKey("event-context.token", PRODUCTION_API), "event-context.token");
});

test("a full-size legacy text file encodes without argument overflow", () => {
  const bytes = new Uint8Array(1 << 20);
  for (let index = 0; index < bytes.length; index += 1) bytes[index] = index % 251;
  const decoded = Buffer.from(bytesToBase64(bytes), "base64");
  assert.equal(decoded.length, bytes.length);
  assert.equal(decoded.compare(Buffer.from(bytes)), 0);
});

test("audio types are restricted and normalized", () => {
  assert.equal(audioMediaType("memo.m4a", ""), "audio/mp4");
  assert.equal(audioMediaType("memo.bin", "audio/x-wav"), "audio/wav");
  assert.equal(audioMediaType("memo.ogg", "audio/ogg"), "");
});

test("changing the workspace locale preserves other URL state", () => {
  const result = new URL(pathWithLocale("https://context.integ.life/workspace.html?locale=en&api=http%3A%2F%2Flocalhost%3A8401&next=records#inbox", "ms"), "https://context.integ.life");
  assert.equal(result.searchParams.get("locale"), "ms");
  assert.equal(result.searchParams.get("api"), "http://localhost:8401");
  assert.equal(result.searchParams.get("next"), "records");
  assert.equal(result.hash, "#inbox");
});

test("event ids use UUIDv7 timestamp, version, and variant bits", () => {
  const id = uuidV7(0x0192f3a17c2e, new Uint8Array(16).fill(0xab));
  assert.match(id, /^0192f3a1-7c2e-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});
