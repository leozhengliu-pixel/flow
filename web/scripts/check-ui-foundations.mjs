import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
const [font, foundations, tokens] = await Promise.all([
  readFile(`${root}/public/fonts/InterVariable.woff2`),
  readFile(`${root}/src/styles/foundations.css`, 'utf8'),
  readFile(`${root}/src/styles/tokens.css`, 'utf8'),
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

for (const color of [
  '--theme-surface-0:lch(5.52% .4 272)',
  '--theme-surface-2:lch(12.72% .85 272)',
  '--theme-border-strong:lch(25.68% 1.93 272)',
  '--theme-text-primary:lch(91.178% 1.425 272)',
]) {
  if (!tokens.includes(color)) throw new Error(`Missing measured dark-theme token: ${color}`)
}
