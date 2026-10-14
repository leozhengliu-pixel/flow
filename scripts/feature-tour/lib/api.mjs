// Small cookie-session API client shared by the seed and recording scripts.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

export const here = path.dirname(fileURLToPath(import.meta.url))
export const tourDir = path.resolve(here, '..')
export const repoRoot = path.resolve(tourDir, '..', '..')
export const stateDir = path.join(tourDir, '.state')

// The app is served from this origin (the Vite dev server proxies /api to the
// Go API). A separate origin from the everyday dev account keeps cookies and
// localStorage apart from whatever else is open on 127.0.0.1.
export const BASE = process.env.FLOW_TOUR_URL || 'http://localhost:5173'
export const WORKSPACE = process.env.FLOW_TOUR_WORKSPACE || 'acme'
export const DOMAIN = 'acme.test'

export const PEOPLE = [
  { key: 'maya', name: 'Maya Chen', role: 'owner' },
  { key: 'daniel', name: 'Daniel Okafor' },
  { key: 'priya', name: 'Priya Raman' },
  { key: 'lucas', name: 'Lucas Meyer' },
  { key: 'sofia', name: 'Sofia Alvarez' },
]
export const emailOf = (key) => `${key}@${DOMAIN}`

export function readCredentials() {
  const file = path.join(stateDir, 'credentials.json')
  if (process.env.FLOW_TOUR_PASSWORD) return { password: process.env.FLOW_TOUR_PASSWORD }
  if (fs.existsSync(file)) return JSON.parse(fs.readFileSync(file, 'utf8'))
  return null
}

export function writeState(name, value) {
  fs.mkdirSync(stateDir, { recursive: true })
  fs.writeFileSync(path.join(stateDir, name), JSON.stringify(value, null, 2), { mode: 0o600 })
}

export function readState(name) {
  const file = path.join(stateDir, name)
  return fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : null
}

export class Client {
  constructor(cookie = '') {
    this.cookie = cookie
  }

  async request(method, url, body, { raw = false } = {}) {
    const response = await fetch(BASE + url, {
      method,
      headers: {
        'content-type': 'application/json',
        'x-workspace-key': WORKSPACE,
        ...(this.cookie ? { cookie: this.cookie } : {}),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const setCookie = response.headers.getSetCookie?.() ?? []
    for (const entry of setCookie) {
      const pair = entry.split(';')[0]
      if (pair.startsWith('flow_clone_session=')) this.cookie = pair
    }
    const text = await response.text()
    let data = text
    try {
      data = text ? JSON.parse(text) : null
    } catch {
      /* plain text */
    }
    if (raw) return { status: response.status, data }
    if (!response.ok) {
      throw new Error(`${method} ${url} -> ${response.status} ${typeof data === 'string' ? data : JSON.stringify(data)}`)
    }
    return data
  }

  get(url) {
    return this.request('GET', url)
  }
  post(url, body = {}) {
    return this.request('POST', url, body)
  }
  patch(url, body = {}) {
    return this.request('PATCH', url, body)
  }
  put(url, body = {}) {
    return this.request('PUT', url, body)
  }
  delete(url) {
    return this.request('DELETE', url)
  }
}

export const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
