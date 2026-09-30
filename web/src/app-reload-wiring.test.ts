import { describe, expect, it } from 'vitest'

// App.tsx is too large to render in a unit test, so guard its wiring at the
// source level: page actions (team and membership pages, customers, reviews,
// labels, cycles, settings, asks, archives) must refresh just what they
// changed, never download the whole workspace bootstrap again.
async function appSource() {
  const fs = (await import(/* @vite-ignore */ `node:${'fs'}`)) as { readFileSync: (path: string, encoding: 'utf8') => string }
  const cwd = (globalThis as unknown as { process: { cwd: () => string } }).process.cwd()
  return fs.readFileSync(`${cwd}/src/App.tsx`, 'utf8')
}

describe('App reload wiring', () => {
  it('uses the workspace bootstrap only for boot, retry, realtime resync and the issue history fallback', async () => {
    const lines = (await appSource()).split('\n')
    const allowed = [/const load = async/, /promise: fetchBootstrap\(requestedWorkspaceKey/, /fetchVisibleIssueIds\(workspace/, /const refreshActivity = async/]
    const offenders = lines.flatMap((line, index) => {
      if (!line.includes('fetchBootstrap(')) return []
      const context = lines.slice(Math.max(0, index - 20), index + 1).join('\n')
      return allowed.some(pattern => pattern.test(context)) ? [] : [`${index + 1}: ${line.trim()}`]
    })
    expect(offenders).toEqual([])
  })

  it('never wires a page onReload to the full bootstrap', async () => {
    const source = await appSource()
    const props = [...source.matchAll(/onReload=\{/g)].map(match => {
      let depth = 0, end = match.index! + 'onReload='.length
      for (; end < source.length; end++) {
        if (source[end] === '{') depth++
        else if (source[end] === '}' && --depth === 0) break
      }
      return source.slice(match.index, end + 1)
    })
    expect(props.length).toBeGreaterThan(10)
    expect(props.filter(prop => /fetchBootstrap|acceptBootstrap|\{load\}/.test(prop))).toEqual([])
  })
})
