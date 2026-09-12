const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { supportedLocales, translations, normalizeLocale, resolveLocale, translate } = require("./i18n.js");

const expectedLocales = ["en", "zh-CN", "ms", "hi"];
assert.deepEqual(supportedLocales, expectedLocales);

const englishKeys = Object.keys(translations.en).sort();
for (const locale of supportedLocales) {
  assert.deepEqual(Object.keys(translations[locale]).sort(), englishKeys, `${locale} translation keys must match English`);
  for (const key of englishKeys) assert.ok(translations[locale][key], `${locale}.${key} must not be empty`);
}

assert.equal(normalizeLocale("zh-SG"), "zh-CN");
assert.equal(normalizeLocale("ms-MY"), "ms");
assert.equal(normalizeLocale("hi-IN"), "hi");
assert.equal(normalizeLocale("en-US"), "en");
assert.equal(normalizeLocale("fr-FR"), "");
assert.equal(resolveLocale("", ["zh-SG", "en-US"]), "zh-CN");
assert.equal(resolveLocale("ms", ["zh-CN"]), "ms");
assert.equal(resolveLocale("", ["fr-FR"]), "en");
assert.equal(resolveLocale("not-a-locale", []), "en");
assert.equal(translate("hi", "signedInAs", { username: "alice" }).includes("alice"), true);

const frontendDir = __dirname;
const index = fs.readFileSync(path.join(frontendDir, "index.html"), "utf8");
const app = fs.readFileSync(path.join(frontendDir, "app.js"), "utf8");
const referencedKeys = new Set();
for (const match of index.matchAll(/data-i18n(?:-placeholder|-aria-label|-content)?="([^"]+)"/g)) referencedKeys.add(match[1]);
for (const match of app.matchAll(/\bt\("([^"]+)"/g)) referencedKeys.add(match[1]);
for (const key of referencedKeys) assert.ok(englishKeys.includes(key), `referenced translation key must exist: ${key}`);

assert.match(index, /<html lang="en">/);
for (const locale of supportedLocales) assert.match(index, new RegExp(`<option value="${locale.replace("-", "\\-")}">`));
assert.match(app, /event_context_locale/);
assert.match(app, /Domain=\.integ\.life/);

console.log(`frontend i18n checks passed: ${supportedLocales.length} locales, ${englishKeys.length} keys`);
