import { createHash } from 'node:crypto'
import { copyFile, mkdir, readdir, readFile, stat, writeFile } from 'node:fs/promises'
import path from 'node:path'
import type { Plugin } from 'vite'

/**
 * Content-hashed build output (`dist/assets/`) differs per release. While replicas of two releases serve side by side
 * (a rolling update, a rollback), a page from one release asks for files only that release has. Two pieces here keep
 * those files available; the Go server reads both (api/cmd/server/static_assets.go):
 *
 * - `releaseManifest()`, a Vite plugin, writes `dist/asset-manifest.json`: the release id and its `assets/` files.
 * - `carryReleaseAssets()` (run by the Dockerfile as `node release-assets.ts carry …`) copies the assets of the
 *   previous image's releases into this image's `/app/web-previous`, served as FLOW_ASSET_PREVIOUS_PATH.
 */
export const MANIFEST_FILE = 'asset-manifest.json'
export const CARRIED_RELEASES_FILE = 'releases.json'

export type ReleaseManifest = { release: string; assets: string[] }

// Vite's output names: one path segment of letters, digits, '.', '_' and '-' (the server accepts the same).
const ASSET_NAME = /^[A-Za-z0-9_-][A-Za-z0-9._-]*$/
const validName = (name: unknown): name is string => typeof name === 'string' && ASSET_NAME.test(name) && !name.includes('..')

/** The release id of a build with these asset names: sha256 of the sorted names joined by newlines (16 hex digits). */
export function releaseId(names: string[]) {
  return createHash('sha256').update([...names].sort().join('\n')).digest('hex').slice(0, 16)
}

async function exists(file: string) {
  return stat(file).then(info => info.isFile() || info.isDirectory(), () => false)
}

async function listAssets(assetsDir: string) {
  const entries = await readdir(assetsDir, { withFileTypes: true }).catch(() => [])
  return entries.filter(entry => entry.isFile() && validName(entry.name)).map(entry => entry.name).sort()
}

/** The release a built `web` directory holds: its manifest, or a listing of `assets/` for a build without one. */
export async function readRelease(webDir: string): Promise<ReleaseManifest | undefined> {
  const manifestFile = path.join(webDir, MANIFEST_FILE)
  if (await exists(manifestFile)) {
    const parsed = JSON.parse(await readFile(manifestFile, 'utf8')) as Partial<ReleaseManifest>
    const assets = (parsed.assets ?? []).filter(validName)
    return { release: validName(parsed.release) ? parsed.release : releaseId(assets), assets }
  }
  const assets = await listAssets(path.join(webDir, 'assets'))
  return assets.length ? { release: releaseId(assets), assets } : undefined
}

/** Writes `<outDir>/asset-manifest.json` for the build in outDir. */
export async function writeReleaseManifest(outDir: string): Promise<ReleaseManifest> {
  const assets = await listAssets(path.join(outDir, 'assets'))
  const manifest = { release: releaseId(assets), assets }
  await writeFile(path.join(outDir, MANIFEST_FILE), `${JSON.stringify(manifest, null, 2)}\n`)
  return manifest
}

export function releaseManifest(): Plugin {
  let outDir = 'dist'
  return {
    name: 'flow-release-manifest',
    apply: 'build',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir)
    },
    async closeBundle() {
      await writeReleaseManifest(outDir)
    },
  }
}

export type CarryOptions = {
  /** This build's output (`dist`). */
  current: string
  /** The previous image's `/app`: its `web/` and, from images with this step, `web-previous/`. */
  previous: string
  /** Where this image's `web-previous` is written. */
  out: string
  /** How many earlier releases to carry. */
  keep: number
}

/**
 * Copies the assets of the newest `keep` releases before this one into `out/assets/` and lists them, newest first,
 * in `out/releases.json`. The previous image's own release comes first, then the releases it carried, so each image
 * holds a sliding window of releases. Files this build has itself are not copied again.
 */
export async function carryReleaseAssets({ current, previous, out, keep }: CarryOptions): Promise<ReleaseManifest[]> {
  const currentRelease = await readRelease(current)
  const own = new Set(currentRelease?.assets ?? [])
  const sources = [path.join(previous, 'web', 'assets'), path.join(previous, 'web-previous', 'assets')]
  const candidates: ReleaseManifest[] = []
  const previousRelease = await readRelease(path.join(previous, 'web'))
  if (previousRelease) candidates.push(previousRelease)
  const carriedFile = path.join(previous, 'web-previous', CARRIED_RELEASES_FILE)
  if (await exists(carriedFile)) {
    const carried = JSON.parse(await readFile(carriedFile, 'utf8')) as Partial<ReleaseManifest>[]
    for (const item of Array.isArray(carried) ? carried : []) {
      if (validName(item.release)) candidates.push({ release: item.release, assets: (item.assets ?? []).filter(validName) })
    }
  }

  const kept: ReleaseManifest[] = []
  const seen = new Set(currentRelease ? [currentRelease.release] : [])
  for (const candidate of candidates) {
    if (kept.length >= keep) break
    if (seen.has(candidate.release)) continue
    seen.add(candidate.release)
    kept.push(candidate)
  }

  await mkdir(path.join(out, 'assets'), { recursive: true })
  const result: ReleaseManifest[] = []
  for (const release of kept) {
    const present: string[] = []
    for (const name of release.assets) {
      if (own.has(name)) {
        present.push(name)
        continue
      }
      const target = path.join(out, 'assets', name)
      if (await exists(target)) {
        present.push(name)
        continue
      }
      for (const dir of sources) {
        const source = path.join(dir, name)
        if (await exists(source)) {
          await copyFile(source, target)
          present.push(name)
          break
        }
      }
    }
    result.push({ release: release.release, assets: present })
  }
  await writeFile(path.join(out, CARRIED_RELEASES_FILE), `${JSON.stringify(result, null, 2)}\n`)
  return result
}

function parseArgs(argv: string[]) {
  const options: Record<string, string> = {}
  for (let i = 0; i < argv.length; i += 2) {
    if (!argv[i].startsWith('--') || argv[i + 1] === undefined) throw new Error(`unexpected argument ${argv[i]}`)
    options[argv[i].slice(2)] = argv[i + 1]
  }
  return options
}

// node release-assets.ts carry --current dist --previous /previous --out /out/web-previous --keep 2
if (import.meta.main) {
  const [command, ...rest] = process.argv.slice(2)
  if (command !== 'carry') {
    console.error('usage: node release-assets.ts carry --current <dist> --previous <image /app> --out <dir> --keep <n>')
    process.exit(2)
  }
  const options = parseArgs(rest)
  const keep = Number(options.keep ?? '2')
  if (!options.current || !options.previous || !options.out || !Number.isInteger(keep) || keep < 0) {
    console.error('carry needs --current, --previous, --out and a whole --keep')
    process.exit(2)
  }
  const carried = await carryReleaseAssets({ current: options.current, previous: options.previous, out: options.out, keep })
  const current = await readRelease(options.current)
  console.log(`release ${current?.release ?? '(none)'} carries ${carried.map(item => `${item.release} (${item.assets.length} files)`).join(', ') || 'no earlier release'}`)
}
