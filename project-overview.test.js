const test = require("node:test");
const assert = require("node:assert/strict");
const { buildProjectOverview, overviewChildKey, filterOverviewChildren, normalizeDeployment } = require("./project-overview.js");

test("integ.life overview exposes the requested project hierarchy", () => {
  const overview = buildProjectOverview({ id: "p1", name: "integ.life", description: "Personal OS" });
  assert.equal(overview.name, "integ.life");
  assert.deepEqual(overview.children.map((item) => item.name), ["Knowledge visual", "Knowledge graph", "Lingulet", "Deployment & traffic"]);
  assert.ok(overview.children.every((item) => item.tasks.length > 0));
  assert.equal(overview.children[2].tasks[0].status, "blocked");
  assert.equal(overview.deployment.status, "unknown");
});

test("deployment metadata is normalized for the overview status card", () => {
  const overview = buildProjectOverview({
    id: "p1",
    name: "integ.life",
    deployment: { status: "failed", commit: "abc1234", branch: "release", route: "https://example.test", checked_at: "2026-10-29T09:00:00Z", blocker: "Smoke check failed" }
  });
  assert.deepEqual(overview.deployment, {
    environment: "production",
    status: "failed",
    commit: "abc1234",
    branch: "release",
    route: "https://example.test",
    checkedAt: "2026-10-29T09:00:00Z",
    blocker: "Smoke check failed"
  });
  assert.equal(normalizeDeployment(null).status, "unknown");
});

test("overview child keys are stable for collapse state", () => {
  assert.equal(overviewChildKey("p1", "visual"), "p1:visual");
});

test("status filter keeps a subproject when one task matches", () => {
  const overview = buildProjectOverview({ id: "p1", name: "integ.life" });
  assert.deepEqual(filterOverviewChildren(overview.children, "blocked").map((item) => item.id), ["lingulet"]);
  assert.deepEqual(filterOverviewChildren(overview.children, "all"), overview.children);
});

test("other projects retain a useful single child workspace", () => {
  const overview = buildProjectOverview({ id: "p2", name: "Research" });
  assert.equal(overview.children.length, 1);
  assert.equal(overview.children[0].id, "context");
});
