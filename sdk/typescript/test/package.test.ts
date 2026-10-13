import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { SDK_VERSION } from '../src/index.js'

const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'))

describe('package', () => {
  it('keeps SDK_VERSION equal to package.json', () => {
    expect(SDK_VERSION).toBe(pkg.version)
  })

  it('has no runtime dependencies', () => {
    expect(pkg.dependencies ?? {}).toEqual({})
    expect(pkg.peerDependencies ?? {}).toEqual({})
  })

  it('exposes the main and webhooks entry points', () => {
    expect(Object.keys(pkg.exports)).toEqual(expect.arrayContaining(['.', './webhooks']))
  })
})
