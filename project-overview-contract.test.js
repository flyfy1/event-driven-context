const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { buildProjectOverview, filterOverviewChildren, normalizeDeployment } = require("./project-overview.js");

const workspaceMarkup = fs.readFileSync(path.join(__dirname, "workspace.html"), "utf8");
const translations = fs.readFileSync(path.join(__dirname, "i18n.js"), "utf8");

test("seeded overview keeps operational status fields reviewable", () => {
  const overview = buildProjectOverview({ id: "p1", name: "integ.life" });
  assert.equal(overview.children.length, 4);
  for (const child of overview.children) {
    assert.ok(child.id, "each subproject has a stable id");
    assert.ok(child.name, `${child.id} has a name`);
    assert.ok(child.status, `${child.id} has a status`);
    assert.ok(child.summary, `${child.id} has a summary`);
    assert.ok(child.activity, `${child.id} has recent activity`);
    assert.ok(Object.prototype.hasOwnProperty.call(child, "blocker"), `${child.id} exposes blocker state`);
    assert.ok(child.next, `${child.id} exposes a next step`);
    assert.ok(Array.isArray(child.tasks) && child.tasks.length > 0, `${child.id} exposes at least one task`);
    for (const task of child.tasks) {
      assert.ok(task.title && task.kind && task.status, `${child.id} tasks are labelled`);
    }
  }
  assert.equal(overview.children.find((item) => item.id === "delivery").status, "monitoring");
  assert.equal(overview.children.find((item) => item.id === "delivery").tasks[0].status, "running");
  assert.equal(overview.children.find((item) => item.id === "lingulet").tasks[0].status, "blocked");
});

test("overview filters preserve the operational subproject boundary", () => {
  const children = buildProjectOverview({ name: "integ.life" }).children;
  assert.deepEqual(filterOverviewChildren(children, "running").map((item) => item.id), ["delivery"]);
  assert.deepEqual(filterOverviewChildren(children, "queued").map((item) => item.id), ["knowledge-graph"]);
  assert.deepEqual(filterOverviewChildren(children, "blocked").map((item) => item.id), ["lingulet"]);
  assert.deepEqual(filterOverviewChildren(children, "all"), children);
});

test("deployment status has an explicit unknown fallback and stable fields", () => {
  assert.deepEqual(normalizeDeployment(), {
    environment: "production",
    status: "unknown",
    commit: "",
    branch: "main",
    route: "",
    checkedAt: "",
    blocker: "Deployment metadata is not connected yet."
  });
  assert.deepEqual(normalizeDeployment({ status: "degraded", commit: "abc1234", branch: "release", route: "https://example.test", checked_at: "2026-10-29T09:00:00Z", blocker: "Health check returned 503." }), {
    environment: "production",
    status: "degraded",
    commit: "abc1234",
    branch: "release",
    route: "https://example.test",
    checkedAt: "2026-10-29T09:00:00Z",
    blocker: "Health check returned 503."
  });
});

test("workspace exposes a labelled filter and live overview region", () => {
  assert.match(workspaceMarkup, /id="overview-filter"/);
  for (const value of ["all", "blocked", "running", "queued"]) {
    assert.match(workspaceMarkup, new RegExp(`option value="${value}"`));
  }
  assert.match(workspaceMarkup, /id="overview-refresh"/);
  assert.match(workspaceMarkup, /id="project-overview"[^>]*aria-live="polite"/);
});

test("overview filter labels are available in the supported UI locales", () => {
  for (const locale of ["en", "zh-CN", "ms", "hi"]) {
    assert.match(translations, new RegExp(`["']?${locale}["']?: \\{[^\\n]*overviewFilterLabel`));
  }
  for (const key of ["overviewFilterAll", "overviewFilterBlocked", "overviewFilterRunning", "overviewFilterQueued", "overviewNoMatches"]) {
    assert.match(translations, new RegExp(key));
  }
});
