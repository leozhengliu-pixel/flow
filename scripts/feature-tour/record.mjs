#!/usr/bin/env node
// Records feature tour clips into docs/media (one MP4 + one GIF per clip).
//
//   node scripts/feature-tour/record.mjs                # every clip, in order
//   node scripts/feature-tour/record.mjs issues-board   # one or more by name
//   node scripts/feature-tour/record.mjs --list
//
// Prerequisites: API on :8080 (FLOW_DEV_AUTH_TOKENS=true, agent enabled for the
// agent clips), web on :5173, `node scripts/feature-tour/seed.mjs` run once.
import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'
import { tourDir } from './lib/api.mjs'
import { connectDemo } from './lib/demo.mjs'
import { createStage } from './lib/stage.mjs'

const clipDir = path.join(tourDir, 'clips')
const available = fs
  .readdirSync(clipDir)
  .filter((file) => file.endsWith('.mjs'))
  .sort()
  .map((file) => file.replace(/\.mjs$/, ''))

const args = process.argv.slice(2)
if (args.includes('--list')) {
  console.log(available.join('\n'))
  process.exit(0)
}
const wanted = args.length && !args.includes('all') ? args : available
for (const name of wanted) if (!available.includes(name)) throw new Error(`unknown clip "${name}"; available: ${available.join(', ')}`)

for (const name of wanted) {
  const clip = (await import(pathToFileURL(path.join(clipDir, `${name}.mjs`)).href)).default
  console.log(`\n== ${name}`)
  const demo = await connectDemo()
  await demo.clearDrafts()
  await clip.prepare?.(demo)
  const startPath = typeof clip.startPath === 'function' ? await clip.startPath(demo) : clip.startPath
  const stage = await createStage({ name, startPath, title: clip.title, subtitle: clip.subtitle, eyebrow: clip.eyebrow, storage: clip.storage, speed: clip.speed, viewport: clip.viewport })
  try {
    await stage.begin()
    await clip.run(stage)
    const result = await stage.finish()
    console.log(`[${name}] wrote ${path.relative(process.cwd(), result.gif)} and ${path.relative(process.cwd(), result.mp4)}`)
  } catch (error) {
    await stage.page.screenshot({ path: path.join(tourDir, '.work', `${name}-error.png`) }).catch(() => {})
    await stage.context.close().catch(() => {})
    console.error(`[${name}] failed:`, error)
    process.exitCode = 1
  }
  // Put the demo data back the way the next take (and the next clip) expects it.
  await demo.clearDrafts()
  await (clip.cleanup ?? clip.prepare)?.(demo).catch((error) => console.warn(`[${name}] reset failed:`, error.message))
}
process.exit(process.exitCode || 0)
