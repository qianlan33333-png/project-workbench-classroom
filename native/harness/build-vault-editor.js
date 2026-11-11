import {readdir, readFile, writeFile} from 'node:fs/promises';
import {build} from 'esbuild';

const result = await build({
  entryPoints: ['harness/vault-editor.js'],
  outfile: 'harness/vault-editor.bundle.js',
  bundle: true,
  format: 'esm',
  target: 'es2022',
  legalComments: 'inline',
  metafile: true,
});
const packageDirectories = new Set(Object.keys(result.metafile.inputs)
  .map(input => input.match(/^(?:.*\/)?node_modules\/(?:@[^/]+\/)?[^/]+/)?.[0])
  .filter(Boolean));
const notices = [];
for (const directory of [...packageDirectories].sort()) {
  const files = (await readdir(directory)).filter(name => /^(licen[cs]e|copying)(\.|$)/i.test(name));
  for (const file of files) {
    notices.push(`${directory}/${file}\n\n${await readFile(`${directory}/${file}`, 'utf8')}`);
  }
}
await writeFile('harness/vault-editor.LICENSES.txt', notices.join('\n\n'));
