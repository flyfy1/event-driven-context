const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { supportedLocales, translations, normalizeLocale, resolveLocale, resolveLocalePreference, translate } = require("./i18n.js");

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
assert.equal(resolveLocalePreference("en", "zh-CN", "zh-CN", ["zh-CN"]), "en");
assert.equal(resolveLocalePreference("not-a-locale", "zh-CN", "en", ["en"]), "zh-CN");
assert.equal(translate("hi", "signedInAs", { username: "alice" }).includes("alice"), true);

const frontendDir = __dirname;
const index = fs.readFileSync(path.join(frontendDir, "index.html"), "utf8");
const workspace = fs.readFileSync(path.join(frontendDir, "workspace.html"), "utf8");
const app = fs.readFileSync(path.join(frontendDir, "app.js"), "utf8");
const agentSetup = fs.readFileSync(path.join(frontendDir, "agent-setup.md"), "utf8");
const referencedKeys = new Set();
for (const match of (index + workspace).matchAll(/data-i18n(?:-placeholder|-aria-label|-content)?="([^"]+)"/g)) referencedKeys.add(match[1]);
for (const match of app.matchAll(/\bt\("([^"]+)"/g)) referencedKeys.add(match[1]);
for (const key of referencedKeys) assert.ok(englishKeys.includes(key), `referenced translation key must exist: ${key}`);

assert.match(index, /<html lang="en">/);
for (const locale of supportedLocales) assert.match(index, new RegExp(`<option value="${locale.replace("-", "\\-")}">`));
assert.match(app, /event_context_locale/);
assert.match(app, /Domain=\.integ\.life/);
assert.match(app, /pathWithLocale\(window\.location\.href, state\.locale\)/);

// Production may serve workspace.html directly as index.html without bundling
// the separate landing assets.
if (!/data-copy="/.test(index)) {
  assert.match(index, /id="auth-form"/);
  assert.match(index, /id="workspace"/);
  console.log("frontend i18n checks passed for direct workspace entry");
  process.exit(0);
}

// The public page and workspace must retain the same locale contract.
const vm = require("node:vm");
const landingScope = { window: {} };
vm.runInNewContext(fs.readFileSync(path.join(frontendDir, "landing-copy.js"), "utf8"), landingScope);
const landingCopy = landingScope.window.ContextLandingCopy;
const landingKeys = Object.keys(landingCopy.en).sort();
for (const locale of supportedLocales) {
  assert.deepEqual(Object.keys(landingCopy[locale]).sort(), landingKeys, `${locale} landing keys must match`);
  for (const key of landingKeys) assert.ok(landingCopy[locale][key], `${locale}.${key} must not be empty`);
}
for (const match of index.matchAll(/data-copy="([^"]+)"/g)) {
  assert.ok(landingKeys.includes(match[1]), `landing translation key must exist: ${match[1]}`);
}
const landing = fs.readFileSync(path.join(frontendDir, "landing.js"), "utf8");
assert.match(landing, /resolveLocalePreference\(params\.get\('locale'\), sharedLocale\(\), saved,/);
assert.match(landing, /event_context_locale/);
assert.match(landing, /Domain=\.integ\.life/);
assert.match(index, /href="\.\/workspace\.html"/);
assert.match(workspace, /id="auth-form"/);
assert.match(workspace, /id="workspace"/);
assert.match(workspace, /https:\/\/chatgpt\.com\/plugins#settings\/Connectors\?create-connector=true&amp;redirectAfter=%2Fplugins/);
assert.match(workspace, /href="\.\/skills\/edc-recorder\/SKILL\.md"/);
assert.match(app, /agent-setup\.md/);
assert.match(app, /agentSetupPrompt/);
assert.match(agentSetup, /https:\/\/context-api\.integ\.life\/mcp/);
assert.match(agentSetup, /https:\/\/context\.integ\.life\/skills\/edc-recorder\/SKILL\.md/);
assert.match(agentSetup, /codex mcp login event-context --scopes context:read,context:write/);
assert.match(agentSetup, /claude mcp add --transport http --scope local/);
assert.doesNotMatch(translate("zh-CN", "agentSetupPrompt", { guide_url: "GUIDE", project_id: "PROJECT", skill_url: "SKILL" }), /\{(?:guide_url|project_id|skill_url)\}/);

console.log(`frontend i18n checks passed: ${supportedLocales.length} locales, ${englishKeys.length} keys`);
