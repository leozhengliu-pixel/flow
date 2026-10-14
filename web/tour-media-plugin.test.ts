import { mkdtemp, readdir, readFile, rm, stat, writeFile } from 'node:fs/promises'
import type { IncomingMessage, ServerResponse } from 'node:http'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { PassThrough } from 'node:stream'
import { afterEach, describe, expect, it } from 'vitest'
import { copyTourMedia, serveTourMedia, TOUR_MEDIA_SOURCE, tourClips, tourMedia } from './tour-media-plugin.ts'

const temporary: string[] = []
async function tempDir() {
  const dir = await mkdtemp(path.join(tmpdir(), 'flow-tour-'))
  temporary.push(dir)
  return dir
}
afterEach(async () => {
  await Promise.all(temporary.splice(0).map(dir => rm(dir, { recursive: true, force: true })))
})

// The clips the Getting started issues embed (tutorialEntries in api/cmd/server/workspace_tutorial_content.go).
const TUTORIAL_CLIPS = [
  'issues-board', 'issue-detail', 'issue-create', 'filters-views', 'inbox',
  'projects-overview', 'initiatives', 'cycles', 'project-update',
  'doc-editing', 'doc-selection-toolbar', 'doc-comment', 'issue-comments',
  'agent-chat', 'agent-floating-chat', 'agent-mention', 'agent-options',
]

describe('tour media', () => {
  it('copies every docs/media GIF into the build output', async () => {
    const outDir = await tempDir()
    const plugin = tourMedia()
    const writeBundle = plugin.writeBundle as (options: { dir?: string }) => Promise<void>
    await writeBundle.call({}, { dir: outDir })
    const copied = (await readdir(path.join(outDir, 'tour'))).sort()
    expect(copied).toEqual(await tourClips())
    expect(copied).toEqual(expect.arrayContaining(TUTORIAL_CLIPS.map(clip => `${clip}.gif`)))
    expect(copied.every(name => name.endsWith('.gif'))).toBe(true)
    const [source, built] = await Promise.all([stat(path.join(TOUR_MEDIA_SOURCE, 'inbox.gif')), stat(path.join(outDir, 'tour', 'inbox.gif'))])
    expect(built.size).toBe(source.size)
  })

  it('copies only GIFs and fails without any', async () => {
    const source = await tempDir()
    await Promise.all([
      writeFile(path.join(source, 'inbox.gif'), 'GIF89a'),
      writeFile(path.join(source, 'inbox.mp4'), 'mp4'),
      writeFile(path.join(source, 'README.md'), '# clips'),
    ])
    const outDir = await tempDir()
    expect(await copyTourMedia(outDir, source)).toEqual(['inbox.gif'])
    expect(await readFile(path.join(outDir, 'tour', 'inbox.gif'), 'utf8')).toBe('GIF89a')
    await expect(copyTourMedia(outDir, await tempDir())).rejects.toThrow(/No tour GIFs/)
  })

  it('serves the clips from the dev server', async () => {
    const source = await tempDir()
    await writeFile(path.join(source, 'inbox.gif'), 'GIF89a')
    const middleware = serveTourMedia(source)
    const request = (url: string) => new Promise<{ status: number; type?: string; body: string; next: boolean }>(resolve => {
      const response = Object.assign(new PassThrough(), { statusCode: 0, headers: {} as Record<string, string>, setHeader(name: string, value: string) { this.headers[name.toLowerCase()] = value } })
      const chunks: Buffer[] = []
      response.on('data', chunk => chunks.push(chunk))
      response.on('end', () => resolve({ status: response.statusCode, type: response.headers['content-type'], body: Buffer.concat(chunks).toString(), next: false }))
      middleware({ url, method: 'GET' } as IncomingMessage, response as unknown as ServerResponse, () => resolve({ status: 0, body: '', next: true }))
    })
    expect(await request('/inbox.gif')).toEqual({ status: 200, type: 'image/gif', body: 'GIF89a', next: false })
    expect((await request('/missing.gif')).next).toBe(true)
    expect((await request('/../package.json')).next).toBe(true)
    expect((await request('/inbox.mp4')).next).toBe(true)
  })
})
