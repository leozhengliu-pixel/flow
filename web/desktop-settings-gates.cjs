const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const root = '/private/tmp/claude-501/-Users-liuzheng-src-flow/412a45aa-8326-428d-9ef9-334bda30c694/scratchpad/codex';
let failed = false;
for (const [gate, area, screen] of [
 ['mobile','settings','nav'], ['mobile','settings','nav-lower'],
 ['text','settings','nav'], ['text','settings','nav-lower'],
 ['mobile','shell','topbar-myissues'], ['mobile','issues-list','my-assigned'],
 ['mobile','issues-list','team-list'], ['mobile','inbox','inbox-list'],
]) {
 const result = spawnSync(process.execPath, [`${root}/${gate}-gate.${gate === 'text' ? 'mjs' : 'mjs'}`, area, '--screen', screen, '--base', 'http://127.0.0.1:5463', '--web', process.cwd()], { encoding:'utf8' });
 const output = result.stdout + result.stderr;
 fs.writeFileSync(`/tmp/codex-desktop-settings/${gate}-${area}-${screen}.log`, output);
 process.stdout.write(output);
 failed ||= result.status !== 0;
}
process.exit(failed ? 1 : 0);
