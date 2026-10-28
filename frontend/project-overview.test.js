const test = require("node:test");
const assert = require("node:assert/strict");
const { buildProjectOverview, overviewChildKey } = require("./project-overview.js");

test("integ.life overview exposes the requested project hierarchy", () => {
  const overview = buildProjectOverview({ id: "p1", name: "integ.life", description: "Personal OS" });
  assert.equal(overview.name, "integ.life");
  assert.deepEqual(overview.children.map((item) => item.name), ["Knowledge visual", "Knowledge graph", "Lingulet", "Deployment & traffic"]);
  assert.ok(overview.children.every((item) => item.tasks.length > 0));
  assert.equal(overview.children[2].tasks[0].status, "blocked");
});

test("overview child keys are stable for collapse state", () => {
  assert.equal(overviewChildKey("p1", "visual"), "p1:visual");
});

test("other projects retain a useful single child workspace", () => {
  const overview = buildProjectOverview({ id: "p2", name: "Research" });
  assert.equal(overview.children.length, 1);
  assert.equal(overview.children[0].id, "context");
});
