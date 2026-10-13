import { describe, expect, it } from 'vitest'
import { Connection, FlowClient } from '../src/index.js'
import { API_URL, json, mockFetch } from './helpers.js'

/** A server with `total` teams, newest first, honouring first/after/last/before like Flow's connections. */
function teamServer(total: number) {
  const all = Array.from({ length: total }, (_, index) => ({ id: `team_${total - index}`, key: `T${total - index}`, name: `Team ${total - index}`, color: '#fff' }))
  return mockFetch(request => {
    const first = request.query.get('first')
    const last = request.query.get('last')
    const after = request.query.get('after')
    const before = request.query.get('before')
    let start = 0
    let end = all.length
    if (after) start = Number(after.replace('c', '')) + 1
    if (before) end = Number(before.replace('c', ''))
    let slice: number[]
    if (last) {
      const from = Math.max(start, end - Number(last))
      slice = Array.from({ length: end - from }, (_, i) => from + i)
    } else {
      const to = Math.min(end, start + Number(first ?? 50))
      slice = Array.from({ length: to - start }, (_, i) => start + i)
    }
    const lo = slice[0] ?? start
    const hi = slice.length ? slice[slice.length - 1] : start - 1
    return json({
      nodes: slice.map(i => all[i]),
      pageInfo: { hasNextPage: hi < all.length - 1, hasPreviousPage: lo > 0, startCursor: slice.length ? `c${lo}` : null, endCursor: slice.length ? `c${hi}` : null },
    })
  })
}

function flowFor(mock: ReturnType<typeof mockFetch>) {
  return new FlowClient({ apiKey: 'k', apiUrl: API_URL, fetch: mock.fetch })
}

describe('connections', () => {
  it('exposes nodes and pageInfo', async () => {
    const mock = teamServer(5)
    const teams = await flowFor(mock).teams({ first: 2 })
    expect(teams).toBeInstanceOf(Connection)
    expect(teams.nodes.map(team => team.id)).toEqual(['team_5', 'team_4'])
    expect(teams.pageInfo).toEqual({ hasNextPage: true, hasPreviousPage: false, startCursor: 'c0', endCursor: 'c1' })
  })

  it('fetchNext appends the next page and updates pageInfo', async () => {
    const mock = teamServer(5)
    const teams = await flowFor(mock).teams({ first: 2 })
    expect(await teams.fetchNext()).toBe(teams)
    expect(teams.nodes.map(team => team.id)).toEqual(['team_5', 'team_4', 'team_3', 'team_2'])
    expect(teams.pageInfo).toMatchObject({ hasNextPage: true, endCursor: 'c3' })
    await teams.fetchNext()
    expect(teams.nodes).toHaveLength(5)
    expect(teams.pageInfo.hasNextPage).toBe(false)
    const calls = mock.calls.length
    await teams.fetchNext() // no-op without a next page
    expect(mock.calls).toHaveLength(calls)
    expect(mock.calls[1].query.get('after')).toBe('c1')
    expect(mock.calls[1].query.get('first')).toBe('2')
  })

  it('keeps the filter and ordering on every page', async () => {
    const mock = teamServer(4)
    const teams = await flowFor(mock).teams({ first: 2, orderBy: 'updatedAt', filter: { key: { startsWith: 'T' } } })
    await teams.fetchNext()
    for (const call of mock.calls) {
      expect(call.query.get('orderBy')).toBe('updatedAt')
      expect(JSON.parse(call.query.get('filter')!)).toEqual({ key: { startsWith: 'T' } })
    }
  })

  it('fetchPrevious prepends the previous page', async () => {
    const mock = teamServer(6)
    const teams = await flowFor(mock).teams({ first: 2, after: 'c1' })
    expect(teams.nodes.map(team => team.id)).toEqual(['team_4', 'team_3'])
    expect(teams.pageInfo.hasPreviousPage).toBe(true)
    await teams.fetchPrevious()
    expect(teams.nodes.map(team => team.id)).toEqual(['team_6', 'team_5', 'team_4', 'team_3'])
    expect(teams.pageInfo.hasPreviousPage).toBe(false)
    expect(mock.calls[1].query.get('last')).toBe('2')
    expect(mock.calls[1].query.get('before')).toBe('c2')
    expect(mock.calls[1].query.get('first')).toBeNull()
  })

  it('pages backward from the end with last/before', async () => {
    const mock = teamServer(5)
    const teams = await flowFor(mock).teams({ last: 2 })
    expect(teams.nodes.map(team => team.id)).toEqual(['team_2', 'team_1'])
    await teams.fetchPrevious()
    expect(teams.nodes.map(team => team.id)).toEqual(['team_4', 'team_3', 'team_2', 'team_1'])
  })

  it('iterates over every page with for await', async () => {
    const mock = teamServer(7)
    const teams = await flowFor(mock).teams({ first: 3 })
    const ids: string[] = []
    for await (const team of teams) ids.push(team.id)
    expect(ids).toEqual(['team_7', 'team_6', 'team_5', 'team_4', 'team_3', 'team_2', 'team_1'])
    expect(mock.calls).toHaveLength(3)
    expect(teams.nodes).toHaveLength(3) // iteration does not modify the connection
  })

  it('iterates only forward from a page fetched with fetchNext', async () => {
    const mock = teamServer(6)
    const teams = await flowFor(mock).teams({ first: 2 })
    await teams.fetchNext()
    const ids: string[] = []
    for await (const team of teams) ids.push(team.id)
    expect(ids).toEqual(['team_6', 'team_5', 'team_4', 'team_3', 'team_2', 'team_1'])
  })

  it('pages() yields one array per page and all() collects with a limit', async () => {
    const mock = teamServer(5)
    const teams = await flowFor(mock).teams({ first: 2 })
    const sizes: number[] = []
    for await (const page of teams.pages()) sizes.push(page.length)
    expect(sizes).toEqual([2, 2, 1])
    expect(await teams.all()).toHaveLength(5)
    expect(await teams.all({ limit: 3 })).toHaveLength(3)
  })

  it('stops when the server repeats a cursor', async () => {
    const stuck = mockFetch(() => json({ nodes: [{ id: 't' }], pageInfo: { hasNextPage: true, hasPreviousPage: false, startCursor: 'c0', endCursor: 'c0' } }))
    const teams = await flowFor(stuck).teams({ first: 1 })
    const seen: unknown[] = []
    for await (const team of teams) seen.push(team)
    expect(seen).toHaveLength(2)
  })

  it('defaults to 50 per page', async () => {
    const mock = teamServer(1)
    await flowFor(mock).teams()
    expect(mock.calls[0].query.get('first')).toBe('50')
  })
})

describe('issues (cursor API)', () => {
  function issueServer(total: number) {
    return mockFetch(request => {
      const limit = Number(request.query.get('limit'))
      const start = request.query.get('cursor') ? Number(request.query.get('cursor')) : 0
      const end = Math.min(total, start + limit)
      return json({
        items: Array.from({ length: end - start }, (_, i) => ({ id: `issue_${start + i}`, identifier: `ENG-${start + i}` })),
        hasMore: end < total,
        nextCursor: end < total ? String(end) : '',
        total,
      })
    })
  }

  it('adapts items/nextCursor to a connection', async () => {
    const mock = issueServer(5)
    const issues = await flowFor(mock).issues({ first: 2 })
    expect(issues.nodes).toHaveLength(2)
    expect(issues.totalCount).toBe(5)
    expect(issues.pageInfo).toMatchObject({ hasNextPage: true, hasPreviousPage: false, endCursor: '2' })
    await issues.fetchNext()
    expect(issues.nodes.map(issue => issue.id)).toEqual(['issue_0', 'issue_1', 'issue_2', 'issue_3'])
    await issues.fetchNext()
    expect(issues.pageInfo.hasNextPage).toBe(false)
    expect(issues.nodes).toHaveLength(5)
  })

  it('iterates all issues and ignores fetchPrevious', async () => {
    const mock = issueServer(5)
    const issues = await flowFor(mock).issues({ first: 2 })
    await issues.fetchPrevious()
    expect(issues.nodes).toHaveLength(2)
    expect((await issues.all()).map(issue => issue.id)).toEqual(['issue_0', 'issue_1', 'issue_2', 'issue_3', 'issue_4'])
  })

  it('starts after a given cursor', async () => {
    const mock = issueServer(5)
    const issues = await flowFor(mock).issues({ first: 2, after: '3' })
    expect(issues.nodes.map(issue => issue.id)).toEqual(['issue_3', 'issue_4'])
    expect(issues.pageInfo.hasNextPage).toBe(false)
  })
})
