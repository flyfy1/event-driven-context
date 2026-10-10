const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { execFileSync } = require('node:child_process');

const root = path.join(__dirname, '..');
const copies = [
  ['backend/plugins/daily-review/skills/daily-review/SKILL.md', 'backend/skills/daily-review/SKILL.md'],
  ['backend/skills/edc-recorder/SKILL.md', 'frontend/skills/edc-recorder/SKILL.md'],
];

for (const [canonical, copy] of copies) {
  test(`${copy} matches its canonical skill`, () => {
    assert.deepEqual(
      fs.readFileSync(path.join(root, copy)),
      fs.readFileSync(path.join(root, canonical)),
      `Edit ${canonical}, then copy it to ${copy}`,
    );
  });
}

test('frontend deployment publishes the recorder and preserves independent skills', t => {
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'edc-skill-publish-'));
  t.after(() => fs.rmSync(work, { recursive: true, force: true }));
  // Replace only Git: exercise the real staging script and rsync without a release.
  fs.writeFileSync(path.join(work, 'git'), `#!/bin/sh
set -eu
case "$1" in
  status) ;;
  rev-parse) printf 'test-revision\\n' ;;
  clone)
    for destination do :; done
    mkdir -p "$destination/skills/independent" "$destination/skills/edc-recorder"
    printf 'keep' > "$destination/skills/independent/SKILL.md"
    printf 'stale' > "$destination/skills/edc-recorder/SKILL.md"
    ;;
  -C)
    case "$3" in
      add) cp -R "$2" "$SKILL_PUBLISH_RESULT" ;;
      diff) exit 0 ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
`, { mode: 0o700 });
  const published = path.join(work, 'published');
  execFileSync('bash', [path.join(root, 'scripts/deploy-frontend.sh')], {
    cwd: root,
    env: { ...process.env, PATH: `${work}:${process.env.PATH}`, SKILL_PUBLISH_RESULT: published },
  });
  for (const skill of ['edc-recorder', 'memory-recall']) {
    assert.deepEqual(
      fs.readFileSync(path.join(published, 'skills', skill, 'SKILL.md')),
      fs.readFileSync(path.join(__dirname, 'skills', skill, 'SKILL.md')),
    );
  }
  assert.equal(fs.readFileSync(path.join(published, 'skills/independent/SKILL.md'), 'utf8'), 'keep');
});
