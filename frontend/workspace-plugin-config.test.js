const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { configurationFor, defaultFor } = require("./workspace-plugin-config.js");

const pluginRoot = path.join(__dirname, "..", "backend", "plugins");
const manifests = new Map(fs.readdirSync(pluginRoot, { withFileTypes: true })
  .filter((entry) => entry.isDirectory())
  .map((entry) => {
    const manifest = JSON.parse(fs.readFileSync(path.join(pluginRoot, entry.name, "manifest.json"), "utf8"));
    return [manifest.id, manifest];
  }));

test("selecting each system plugin returns its own prepopulated configuration", () => {
  const daily = configurationFor(manifests.get("daily-review"));
  const audio = configurationFor(manifests.get("audio-transcribe"));
  const evidence = configurationFor(manifests.get("evidence"));

  assert.deepEqual(JSON.parse(daily.json), {
    time: "21:00",
    language: "auto",
    prompt: "Keep progress, decisions, todos, questions, and suggestions distinct."
  });
  assert.deepEqual(JSON.parse(audio.json), {
    language: "auto",
    prompt: "Transcribe faithfully in the original language and mark unclear speech without guessing."
  });
  assert.equal(evidence.json, "{}");
  assert.notEqual(audio.json, daily.json);
});

test("all system plugin defaults have matching field documentation", () => {
  assert.equal(manifests.size, 5);
  for (const [id, manifest] of manifests) {
    const view = configurationFor(manifest);
    const configKeys = Object.keys(view.config).sort();
    const documentedKeys = view.fields.map((field) => field.key).sort();
    assert.deepEqual(documentedKeys, configKeys, `${id} documentation must match its configuration`);
    for (const field of view.fields) {
      assert.ok(field.type, `${id}.${field.key} type must be documented`);
      assert.ok(field.description, `${id}.${field.key} description must be documented`);
      assert.notEqual(defaultFor(view.config, field.key), null, `${id}.${field.key} default must be shown`);
    }
  }
});

test("configuration-free plugins are represented explicitly", () => {
  for (const id of ["evidence", "notes-indexer"]) {
    const view = configurationFor(manifests.get(id));
    assert.equal(view.json, "{}");
    assert.deepEqual(view.fields, []);
  }
});

test("the workspace selector replaces the textarea and help when plugins change", () => {
  class Element {
    constructor() {
      this.children = [];
      this.classList = {
        values: new Set(),
        toggle: (name, force) => force ? this.classList.values.add(name) : this.classList.values.delete(name),
        contains: (name) => this.classList.values.has(name)
      };
      this.textContent = "";
      this.value = "";
    }
    append(...children) { this.children.push(...children); }
    replaceChildren(...children) { this.children = children; }
    get lastChild() { return this.children.at(-1); }
  }

  const selectors = [
    "#plugin-select", "#plugin-catalog-preview", "#plugin-install-submit", "#plugin-config-help",
    "#plugin-config-fields", "#plugin-config-none", "#plugin-catalog-name", "#plugin-catalog-version",
    "#plugin-catalog-description", "#plugin-catalog-permissions", "#plugin-config"
  ];
  const elements = new Map(selectors.map((selector) => [selector, new Element()]));
  const app = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
  const renderer = app.slice(app.indexOf("function selectedSystemPlugin()"), app.indexOf("function renderPlugins()"));
  const scope = {
    state: { systemPlugins: [...manifests.values()], plugins: [], selectedPluginID: "", pluginsStatus: "ready" },
    $: (selector) => elements.get(selector),
    configurationFor,
    defaultFor,
    document: { createElement: () => new Element(), createTextNode: (textContent) => ({ textContent }) },
    t: (key, values) => values && values.name ? `${values.name} (installed)` : key
  };
  vm.runInNewContext(renderer, scope);

  vm.runInNewContext('chooseSystemPlugin("daily-review")', scope);
  assert.deepEqual(JSON.parse(elements.get("#plugin-config").value), manifests.get("daily-review").config);
  assert.equal(elements.get("#plugin-config-fields").children.length, 3);
  assert.equal(elements.get("#plugin-config-none").classList.contains("hidden"), true);

  vm.runInNewContext('chooseSystemPlugin("project-brief")', scope);
  assert.deepEqual(JSON.parse(elements.get("#plugin-config").value), manifests.get("project-brief").config);
  assert.equal(elements.get("#plugin-config-fields").children.length, 2);

  vm.runInNewContext('chooseSystemPlugin("notes-indexer")', scope);
  assert.equal(elements.get("#plugin-config").value, "{}");
  assert.equal(elements.get("#plugin-config-fields").children.length, 0);
  assert.equal(elements.get("#plugin-config-none").classList.contains("hidden"), false);
});
