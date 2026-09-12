import { build } from 'esbuild';
import { readFile, writeFile, access } from 'node:fs/promises';
import { dirname, resolve, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(fileURLToPath(import.meta.url));
const result = await build({
  absWorkingDir: root, entryPoints: ['editor.js'], bundle: true, minify: true,
  format: 'iife', globalName: 'ContextCodeEditor', outfile: '../vendor/code-editor.js',
  legalComments: 'linked', metafile: true
});
// Include licenses for the packages actually present in the delivered bundle.
const packages = new Map();
for (const input of Object.keys(result.metafile.inputs)) {
  if (!input.includes('node_modules/')) continue;
  let directory = dirname(resolve(root, input));
  while (directory !== root) {
    try {
      const info = JSON.parse(await readFile(resolve(directory, 'package.json'), 'utf8'));
      if (info.name && !packages.has(info.name)) {
        let license;
        for (const name of ['LICENSE', 'LICENSE.txt', 'LICENSE.md', 'license']) {
          try { await access(resolve(directory, name)); license = await readFile(resolve(directory, name), 'utf8'); break; } catch {}
        }
        if (!license) throw new Error('Missing license: ' + info.name);
        packages.set(info.name, `${info.name} ${info.version}\n${license.trim()}`);
      }
      break;
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
      directory = dirname(directory);
    }
  }
}
await writeFile(resolve(root, '../vendor/code-editor.LICENSE.txt'), [...packages.values()].sort().join('\n\n---\n\n') + '\n');
console.log(`Built ${relative(root, resolve(root, '../vendor/code-editor.js'))}; included ${packages.size} dependency licenses.`);
