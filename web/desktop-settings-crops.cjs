const { execFileSync } = require('node:child_process');
const compare = '/private/tmp/claude-501/-Users-liuzheng-src-flow/412a45aa-8326-428d-9ef9-334bda30c694/scratchpad/codex/desktop-compare.cjs';
for (const [name, ref, route, x, y, w, h] of [
 ['profile','profile','account/profile',900,175,250,150],
 ['integrations-enabled','integrations','integrations',518,170,640,198],
 ['integrations-discovery','integrations','integrations',518,411,640,429],
 ['team-overview','team-settings','teams/VER',518,105,640,330],
 ['nav-footer','nav-admin','account/preferences',0,850,244,50],
 ['teams','teams','teams',290,112,1110,170],
 ['labels','issue-labels','issue-labels',1010,157,377,80],
 ['sessions','account-security','account/security',518,195,640,75],
 ['notifications','notifications','account/notifications',518,458,640,270],
 ['workspace','workspace','workspace',518,374,640,429],
 ['api','api','api',518,108,640,380],
 ['preferences','preferences','account/preferences',1060,210,90,90],
]) {
 execFileSync(process.execPath, [compare,`/tmp/desktop-ref/settings/${ref}-ref.png`,`http://127.0.0.1:5463/verify/settings/${route}`,x,y,w,h,`/tmp/codex-desktop-settings/shots/${name}.png`].map(String), {stdio:'inherit'});
 process.stdout.write(`${name}.png\n`);
}
