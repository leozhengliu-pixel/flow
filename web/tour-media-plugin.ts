import { createReadStream } from 'node:fs'
import { copyFile, mkdir, readdir, stat } from 'node:fs/promises'
import type { IncomingMessage, ServerResponse } from 'node:http'
import path from 'node:path'
import type { Plugin } from 'vite'

/**
 * The "Getting started" issues a new workspace starts with (api/cmd/server/workspace_tutorial.go) embed the
 * feature-tour clips as `/tour/<clip>.gif`. The GIFs live once, in docs/media; this plugin copies them into the build
 * (`dist/tour/`, served by the Go server's FLOW_STATIC_PATH like any other asset) and serves them in `vite dev`, so the
 * tutorial works offline and on self-hosted deployments without a second copy in git.
 */
export const TOUR_MEDIA_SOURCE = path.resolve(import.meta.dirname, '../docs/media')
export const TOUR_MEDIA_ROUTE = '/tour/'

const CLIP = /^[a-z0-9][a-z0-9-]*\.gif$/

/** The tour GIFs in `sourceDir`, sorted. */
export async function tourClips(sourceDir = TOUR_MEDIA_SOURCE): Promise<string[]> {
  const entries = await readdir(sourceDir, { withFileTypes: true })
  return entries.filter(entry => entry.isFile() && CLIP.test(entry.name)).map(entry => entry.name).sort()
}

/** Copies the tour GIFs into `<outDir>/tour/` and returns their names. */
export async function copyTourMedia(outDir: string, sourceDir = TOUR_MEDIA_SOURCE): Promise<string[]> {
  const clips = await tourClips(sourceDir)
  if (!clips.length) throw new Error(`No tour GIFs in ${sourceDir}; the Getting started issues embed them from ${TOUR_MEDIA_ROUTE}.`)
  const target = path.join(outDir, TOUR_MEDIA_ROUTE)
  await mkdir(target, { recursive: true })
  await Promise.all(clips.map(clip => copyFile(path.join(sourceDir, clip), path.join(target, clip))))
  return clips
}

/** Dev-server middleware for `/tour/<clip>.gif`, mounted under TOUR_MEDIA_ROUTE (the URL it sees has the prefix removed). */
export function serveTourMedia(sourceDir = TOUR_MEDIA_SOURCE) {
  return (request: IncomingMessage, response: ServerResponse, next: (error?: unknown) => void) => {
    // Clip names are plain ASCII, so the path needs no decoding (and cannot climb out of sourceDir).
    const name = (request.url ?? '').split('?')[0].replace(/^\/+/, '')
    if (!CLIP.test(name) || (request.method !== 'GET' && request.method !== 'HEAD')) return next()
    const file = path.join(sourceDir, name)
    stat(file).then(info => {
      if (!info.isFile()) return next()
      response.statusCode = 200
      response.setHeader('Content-Type', 'image/gif')
      response.setHeader('Content-Length', String(info.size))
      response.setHeader('Cache-Control', 'no-cache')
      if (request.method === 'HEAD') return response.end()
      createReadStream(file).on('error', next).pipe(response)
    }, () => next())
  }
}

export function tourMedia(sourceDir = TOUR_MEDIA_SOURCE): Plugin {
  return {
    name: 'flow-tour-media',
    configureServer(server) {
      server.middlewares.use(TOUR_MEDIA_ROUTE, serveTourMedia(sourceDir))
    },
    async writeBundle(options) {
      if (options.dir) await copyTourMedia(options.dir, sourceDir)
    },
  }
}
