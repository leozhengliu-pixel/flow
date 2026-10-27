import { cp, mkdir, mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { CARRIED_RELEASES_FILE, MANIFEST_FILE, carryReleaseAssets, readRelease, releaseId, releaseManifest, writeReleaseManifest } from './release-assets.ts'

const temporary: string[] = []
async function tempDir() {
  const dir = await mkdtemp(path.join(tmpdir(), 'flow-release-'))
  temporary.push(dir)
  return dir
}
afterEach(async () => {
  await Promise.all(temporary.splice(0).map(dir => rm(dir, { recursive: true, force: true })))
})

/** A `vite build` output with these asset files (and its manifest unless withoutManifest). */
async function build(assets: string[], { withoutManifest = false } = {}) {
  const dist = await tempDir()
  await mkdir(path.join(dist, 'assets'), { recursive: true })
  await Promise.all(assets.map(name => writeFile(path.join(dist, 'assets', name), `/* ${name} */`)))
  await writeFile(path.join(dist, 'index.html'), '<!doctype html>')
  if (!withoutManifest) await writeReleaseManifest(dist)
  return dist
}

/** What the Dockerfile does: an image's /app with web/ = dist and web-previous/ carried from the image before. */
async function image(dist: string, previousImage: string | undefined, keep = 2) {
  const app = await tempDir()
  await cp(dist, path.join(app, 'web'), { recursive: true })
  const previous = previousImage ?? (await tempDir())
  await carryReleaseAssets({ current: dist, previous, out: path.join(app, 'web-previous'), keep })
  return app
}

const files = async (dir: string) => (await readdir(dir).catch(() => [])).sort()

describe('release assets', () => {
  it('ids a release like the server does', () => {
    // api/cmd/server/static_assets.go releaseID: sha256 of the sorted names joined by newlines, 16 hex digits.
    expect(releaseId(['route-AAAA2222.js', 'index-AAAA1111.js'])).toBe('550de73505de6aee')
  })

  it('writes the manifest after the build', async () => {
    const dist = await build(['index-AAAA1111.js', 'route-AAAA2222.js'], { withoutManifest: true })
    const plugin = releaseManifest() as unknown as { configResolved(config: unknown): void; closeBundle(): Promise<void> }
    plugin.configResolved({ root: dist, build: { outDir: '.' } })
    await plugin.closeBundle()
    expect(JSON.parse(await readFile(path.join(dist, MANIFEST_FILE), 'utf8'))).toEqual({ release: '550de73505de6aee', assets: ['index-AAAA1111.js', 'route-AAAA2222.js'] })
  })

  it('lists the assets of a build made before manifests', async () => {
    const dist = await build(['index-AAAA1111.js', 'route-AAAA2222.js', '.hidden'], { withoutManifest: true })
    expect(await readRelease(dist)).toEqual({ release: '550de73505de6aee', assets: ['index-AAAA1111.js', 'route-AAAA2222.js'] })
  })

  it('carries a sliding window of earlier releases from image to image', async () => {
    const v1 = await build(['index-AAAA1111.js', 'route-AAAA2222.js', 'vendor-SAME0000.js'])
    const v2 = await build(['index-BBBB1111.js', 'route-BBBB2222.js', 'vendor-SAME0000.js'])
    const v3 = await build(['index-CCCC1111.js', 'route-CCCC2222.js', 'vendor-SAME0000.js'])
    const v4 = await build(['index-DDDD1111.js', 'route-DDDD2222.js'])

    const first = await image(v1, undefined)
    expect(await files(path.join(first, 'web-previous', 'assets'))).toEqual([])

    const second = await image(v2, first)
    // v2's image serves v1's chunks; the vendor file both share is not copied twice.
    expect(await files(path.join(second, 'web-previous', 'assets'))).toEqual(['index-AAAA1111.js', 'route-AAAA2222.js'])

    const third = await image(v3, second)
    expect(await files(path.join(third, 'web-previous', 'assets'))).toEqual(['index-AAAA1111.js', 'index-BBBB1111.js', 'route-AAAA2222.js', 'route-BBBB2222.js'])

    const fourth = await image(v4, third)
    // keep = 2: v1 falls out. The shared vendor file comes from v3's own assets.
    expect(await files(path.join(fourth, 'web-previous', 'assets'))).toEqual(['index-BBBB1111.js', 'index-CCCC1111.js', 'route-BBBB2222.js', 'route-CCCC2222.js', 'vendor-SAME0000.js'])
    const carried = JSON.parse(await readFile(path.join(fourth, 'web-previous', CARRIED_RELEASES_FILE), 'utf8'))
    expect(carried.map((item: { release: string }) => item.release)).toEqual([(await readRelease(v3))!.release, (await readRelease(v2))!.release])
  })

  it('carries from an image built before this step', async () => {
    const legacyDist = await build(['index-AAAA1111.js', 'route-AAAA2222.js'], { withoutManifest: true })
    const legacy = await tempDir()
    await cp(legacyDist, path.join(legacy, 'web'), { recursive: true })
    const next = await image(await build(['index-BBBB1111.js']), legacy)
    expect(await files(path.join(next, 'web-previous', 'assets'))).toEqual(['index-AAAA1111.js', 'route-AAAA2222.js'])
  })

  it('does not carry the release being built (a rebuild of the same commit)', async () => {
    const v1 = await build(['index-AAAA1111.js'])
    const first = await image(v1, undefined)
    const again = await image(await build(['index-AAAA1111.js']), first)
    expect(JSON.parse(await readFile(path.join(again, 'web-previous', CARRIED_RELEASES_FILE), 'utf8'))).toEqual([])
  })

  it('ignores names that could leave the assets directory', async () => {
    const previous = await tempDir()
    await mkdir(path.join(previous, 'web-previous'), { recursive: true })
    await writeFile(path.join(previous, 'web-previous', CARRIED_RELEASES_FILE), JSON.stringify([{ release: 'r1', assets: ['../../etc/passwd', 'ok-AAAA1111.js'] }, { release: '../x', assets: [] }]))
    const out = await tempDir()
    const carried = await carryReleaseAssets({ current: await build(['index-BBBB1111.js']), previous, out, keep: 5 })
    expect(carried).toEqual([{ release: 'r1', assets: [] }])
  })
})
