import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const [font, foundations, tokens, cycles, projects, teamOverview, documents] = await Promise.all([
  readFile(`${root}/public/fonts/InterVariable.woff2`),
  readFile(`${root}/src/styles/foundations.css`, 'utf8'),
  readFile(`${root}/src/styles/tokens.css`, 'utf8'),
  readFile(`${root}/src/components/cycles/cycles.css`, 'utf8'),
  readFile(`${root}/src/components/projects-page/projects-page.css`, 'utf8'),
  readFile(`${root}/src/components/team-overview/team-overview-page.css`, 'utf8'),
  readFile(`${root}/src/components/documents/document-page.css`, 'utf8'),
])

const expectedFontHash = '693b77d4f32ee9b8bfc995589b5fad5e99adf2832738661f5402f9978429a8e3'
const actualFontHash = createHash('sha256').update(font).digest('hex')
if (actualFontHash !== expectedFontHash) throw new Error(`InterVariable.woff2 hash changed: ${actualFontHash}`)

for (const token of [
  '--font-weight-normal: 450',
  '--speed-quick-transition: .1s',
  '--speed-regular-transition: .25s',
  '--radius-rounded: 9999px',
  '--ease-out-quad: cubic-bezier(.25,.46,.45,.94)',
]) {
  if (!foundations.includes(token)) throw new Error(`Missing measured foundation token: ${token}`)
}

for (const alias of [
  '--theme-background:var(--bg-panel)',
  '--theme-shadow-popover:var(--menu-shadow)',
  '--theme-accent:var(--accent-primary)',
  '--text-primary:var(--theme-text-primary)',
]) {
  if (!tokens.includes(alias)) throw new Error(`Missing semantic theme alias: ${alias}`)
}

if (!cycles.includes('.flow-cycles-page,.flow-cycle-detail{height:calc(100% - 16px);margin:8px 8px 8px 0}')) {
  throw new Error('Cycle surfaces must use the shared 8px framed-workspace inset')
}
if (!projects.includes('.lp-project-row__name strong { flex: none; font-size: 13px; font-weight: 500; line-height: 18.5px; }')) {
  throw new Error('Project row title typography drifted from the measured Linear line box')
}
if (!teamOverview.includes('isolation: isolate;') || !teamOverview.includes('z-index: -1;')) {
  throw new Error('Team resource menu hover layers must remain behind direct text nodes')
}
if (!documents.includes('.document-meta-avatar {') || !documents.includes('background: var(--chart-cyan);') || !documents.includes('border-radius: 50%;')) {
  throw new Error('Document metadata avatars must retain the measured 18px circular treatment')
}
if (!documents.includes('.document-icon.is-empty {') || !documents.includes('width: 32px;') || !documents.includes('height: 36px;')) {
  throw new Error('Empty-document icon must retain the measured Retina-corrected 32x36 treatment')
}

for (const color of [
  '--theme-surface-0:lch(5.52% .4 272)',
  '--theme-surface-2:lch(12.72% .85 272)',
  '--theme-border-strong:lch(25.68% 1.93 272)',
  '--theme-text-primary:lch(91.178% 1.425 272)',
]) {
  if (!tokens.includes(color)) throw new Error(`Missing measured dark-theme token: ${color}`)
}
