const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const css = fs.readFileSync(path.join(__dirname, "workspace-features.css"), "utf8");
const mobileStart = css.indexOf("@media (max-width: 560px)");
const mobileEnd = css.indexOf("\n.section-heading-inline {", mobileStart);
const mobileBlock = css.slice(mobileStart, mobileEnd > mobileStart ? mobileEnd : css.length);

test("overview mobile layout keeps filter controls and metrics reachable", () => {
  assert.match(mobileBlock, /\.section-heading-inline\s*\{[\s\S]*flex-direction:\s*column/);
  assert.match(mobileBlock, /\.overview-toolbar\s*\{[\s\S]*width:\s*100%/);
  assert.match(mobileBlock, /\.overview-toolbar select\s*\{[\s\S]*min-height:\s*44px/);
  assert.match(mobileBlock, /\.overview-metrics\s*\{[\s\S]*grid-template-columns:\s*repeat\(2/);
  assert.match(mobileBlock, /\.overview-tree\s*\{[\s\S]*padding-left:\s*10px/);
});

test("overview action buttons remain touch-sized on compact screens", () => {
  assert.match(mobileBlock, /\.overview-toolbar \.quiet\s*\{[\s\S]*min-height:\s*44px/);
  assert.match(css, /@media \(max-width: 700px\) \{[\s\S]*\.overview-child-actions \.quiet\s*\{[\s\S]*flex:\s*1 1 0/);
});
