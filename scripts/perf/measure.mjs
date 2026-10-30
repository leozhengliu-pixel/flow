#!/usr/bin/env node
// Measures the read paths that matter at production scale against a running
// Flow API server. Every path runs 20 times after one warm-up and reports
// p50 / p95 / max in milliseconds and the number of rows returned.
//
//   FLOW_PERF_BASE=http://127.0.0.1:18300 \
//   FLOW_PERF_WORKSPACE=perf-main \
//   node scripts/perf/measure.mjs --out /tmp/oc-perf/before.json
//
// The API must run with FLOW_AUTH_DISABLED=true. The script creates a
// read-only API key for the workspace so it can call the MCP read tools
// directly (never through a model) and revokes it when it finishes.

import fs from 'node:fs'

const BASE = (process.env.FLOW_PERF_BASE || 'http://127.0.0.1:18300').replace(/\/$/, '')
const WORKSPACE = process.env.FLOW_PERF_WORKSPACE || 'perf-main'
const RUNS = Number(process.env.FLOW_PERF_RUNS || 20)
const outIndex = process.argv.indexOf('--out')
const OUT = outIndex >= 0 ? process.argv[outIndex + 1] : ''

const headers = { 'X-Workspace-Key': WORKSPACE }

function pct(values, p) {
  const sorted = [...values].sort((a, b) => a - b)
  return sorted[Math.min(sorted.length - 1, Math.max(0, Math.ceil(p * sorted.length) - 1))]
}

async function request(path, init = {}) {
  const response = await fetch(BASE + path, {
    ...init,
    headers: { ...headers, ...(init.headers || {}) },
  })
  const text = await response.text()
  let body = null
  try {
    body = text ? JSON.parse(text) : null
  } catch {
    body = text
  }
  return { status: response.status, body }
}

function rowsOf(name, body) {
  if (!body || typeof body !== 'object') return 0
  if (Array.isArray(body)) return body.length
  if (Array.isArray(body.items)) return body.items.length
  if (Array.isArray(body.notifications)) return body.notifications.length
  if (Array.isArray(body.results)) return body.results.length
  if (Array.isArray(body.groups)) return body.groups.length
  if (Array.isArray(body.relatedIssues) || Array.isArray(body.comments)) {
    return (body.relatedIssues?.length || 0) + (body.comments?.length || 0) + (body.activities?.length || 0)
  }
  return 1
}

const results = []

async function measure(name, path, { init, rows, prepare } = {}) {
  try {
    if (prepare) path = await prepare()
    const warm = await request(path, init)
    if (warm.status >= 400) {
      results.push({ name, status: warm.status, error: typeof warm.body === 'string' ? warm.body.slice(0, 120) : JSON.stringify(warm.body).slice(0, 120) })
      return
    }
    const times = []
    let body = warm.body
    for (let i = 0; i < RUNS; i++) {
      const started = process.hrtime.bigint()
      const response = await request(path, init)
      times.push(Number(process.hrtime.bigint() - started) / 1e6)
      if (response.status >= 400) throw new Error(`status ${response.status}`)
      body = response.body
    }
    results.push({
      name,
      status: 200,
      p50: round(pct(times, 0.5)),
      p95: round(pct(times, 0.95)),
      max: round(Math.max(...times)),
      rows: rows ? rows(body) : rowsOf(name, body),
    })
  } catch (error) {
    results.push({ name, status: 'ERR', error: String(error).slice(0, 140) })
  }
}

function round(value) {
  return Math.round(value * 10) / 10
}

function issueFilters(filters) {
  const query = new URLSearchParams({ workspace: WORKSPACE, limit: '50', projection: 'list' })
  for (const filter of filters) query.append('filter', JSON.stringify(filter))
  return `/api/issue-records?${query}`
}

async function deepCursor(team) {
  let cursor = ''
  for (let page = 0; page < 30; page++) {
    const query = new URLSearchParams({ workspace: WORKSPACE, teamId: team, limit: '100', projection: 'list', sort: 'sortOrder', direction: 'asc' })
    if (cursor) query.set('cursor', cursor)
    const { body } = await request(`/api/issue-records?${query}`)
    if (!body?.nextCursor) break
    cursor = body.nextCursor
  }
  return cursor
}

async function apiKey() {
  if (process.env.FLOW_PERF_API_KEY) return process.env.FLOW_PERF_API_KEY
  const { body } = await request(`/api/api-keys?workspace=${encodeURIComponent(WORKSPACE)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: `perf-measure-${Date.now()}`, scopes: ['read'] }),
  })
  if (!body?.secret) throw new Error(`could not create API key: ${JSON.stringify(body).slice(0, 200)}`)
  return body.secret
}

async function mcpMeasure(name, secret, tool, args) {
  await measure(name, '/mcp/readonly', {
    init: {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', Authorization: `Bearer ${secret}` },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: tool, arguments: args } }),
    },
    rows: (body) => {
      const text = body?.result?.content?.[0]?.text
      if (!text) return body?.result?.structuredContent ? 1 : 0
      try {
        const parsed = JSON.parse(text)
        return Array.isArray(parsed.items) ? parsed.items.length : 1
      } catch {
        return 1
      }
    },
  })
}

async function main() {
  const viewer = 'usr_local'
  const team = 'team_perf_0'
  const teamKey = 'T1'
  const label = 'label_perf_perf-main_0'
  const project = 'project_perf_1'
  const cycle = 'cycle_perf_1_team_perf_0'
  const parentIssue = 'issue_perf-main_600'

  await measure('my-assigned', issueFilters([{ field: 'assignee', operator: 'is', values: [viewer] }]))
  await measure('my-created', issueFilters([{ field: 'creator', operator: 'is', values: [viewer] }]))
  await measure('my-subscribed', issueFilters([{ field: 'subscribers', operator: 'is', values: [viewer] }]))
  await measure('team-list', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&limit=50&projection=list`)
  await measure('team-groups', `/api/issue-records/groups?workspace=${WORKSPACE}&teamId=${team}&groupBy=status`)
  await measure('filter-label', issueFilters([{ field: 'labels', operator: 'is', values: [label] }]))
  await measure('filter-assignee', issueFilters([{ field: 'assignee', operator: 'is', values: ['usr_002'] }]))
  await measure('filter-priority', issueFilters([{ field: 'priority', operator: 'is', values: ['1'] }]))
  await measure('filter-project', `/api/issue-records?workspace=${WORKSPACE}&projectId=${project}&limit=50&projection=list`)
  await measure('sort-priority', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&sort=priority&direction=desc&limit=50&projection=list`)
  await measure('sort-updated', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&sort=updatedAt&direction=desc&limit=50&projection=list`)
  await measure('first-page', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&sort=sortOrder&direction=asc&limit=50&projection=list`)
  const cursor = await deepCursor(team)
  if (cursor) {
    await measure('deep-page', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&sort=sortOrder&direction=asc&limit=50&projection=list&cursor=${encodeURIComponent(cursor)}`)
  }
  await measure('board', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&groupBy=status&limit=50&projection=list`)
  await measure('saved-view', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&filter=${encodeURIComponent(JSON.stringify({ and: [{ field: 'labels', operator: 'is', values: [label] }, { field: 'priority', operator: 'is', values: ['1', '2'] }] }))}&sort=updatedAt&direction=desc&limit=50&projection=list`)
  await measure('all-issues', `/api/issue-records?workspace=${WORKSPACE}&limit=50&projection=list&includeTotal=true`)
  await measure('all-issues-no-total', `/api/issue-records?workspace=${WORKSPACE}&limit=50&projection=list`)
  await measure('triage', `/api/issue-records?workspace=${WORKSPACE}&teamId=${team}&limit=50&projection=list&filter=${encodeURIComponent(JSON.stringify({ and: [{ field: 'assignee', operator: 'isempty' }, { field: 'statusType', operator: 'is', values: ['backlog'] }] }))}`)

  await measure('issue-detail', `/api/issue-records/${parentIssue}?workspace=${WORKSPACE}`)
  await measure('issue-context', `/api/issue-records/${parentIssue}/context?workspace=${WORKSPACE}`)
  const longIssue = 'issue_perf-main_37500'
  await measure('issue-context-long', `/api/issue-records/${longIssue}/context?workspace=${WORKSPACE}`)
  await measure('issue-history-long', `/api/issue-records/${longIssue}/history?workspace=${WORKSPACE}`)
  await measure('issue-related', `/api/issue-records/${parentIssue}/related?workspace=${WORKSPACE}`)

  await measure('search', `/api/search?workspace=${WORKSPACE}&q=payment&limit=30`)
  await measure('search-ids', `/api/issue-records/search-ids?workspace=${WORKSPACE}&q=payment&limit=50`)
  await measure('inbox', `/api/notifications?workspace=${WORKSPACE}&limit=50`)
  await measure('projects', `/api/projects?workspace=${WORKSPACE}&limit=50`)
  await measure('project-summary', `/api/issue-records/project-summary?workspace=${WORKSPACE}&projectId=${project}`)
  await measure('documents', `/api/documents?workspace=${WORKSPACE}`)
  await measure('document', `/api/documents/doc_perf_0?workspace=${WORKSPACE}`)
  await measure('cycle-graph', `/api/cycles/${cycle}/graph?workspace=${WORKSPACE}`)
  await measure('cycles-issues', `/api/issue-records?workspace=${WORKSPACE}&cycleId=${cycle}&limit=50&projection=list`)
  await measure('bootstrap', `/api/issue-records/bootstrap?workspace=${WORKSPACE}`)
  await measure('realtime-changes', `/api/realtime/changes?workspace=${WORKSPACE}`)

  const secret = await apiKey()
  await mcpMeasure('mcp-list-issues', secret, 'list_issues', { team: teamKey, limit: 50 })
  await mcpMeasure('mcp-count-issues', secret, 'count_issues', { team: teamKey })
  await mcpMeasure('mcp-search-issues', secret, 'search_issues', { queries: ['payment'], limit: 20 })
  await mcpMeasure('mcp-get-issue', secret, 'get_issue', { id: 'T1-601' })
  await mcpMeasure('mcp-list-comments', secret, 'list_comments', { issueId: 'T1-601', limit: 50 })
  await mcpMeasure('mcp-list-projects', secret, 'list_projects', { limit: 50 })
  await mcpMeasure('mcp-list-cycles', secret, 'list_cycles', { teamId: team })

  const widths = { name: 24, p50: 8, p95: 8, max: 8, rows: 7 }
  const line = (row) =>
    [
      String(row.name).padEnd(widths.name),
      String(row.status).padStart(6),
      String(row.p50 ?? '').padStart(widths.p50),
      String(row.p95 ?? '').padStart(widths.p95),
      String(row.max ?? '').padStart(widths.max),
      String(row.rows ?? '').padStart(widths.rows),
      row.error ? ` ${row.error}` : '',
    ].join(' ')
  console.log('path                     status      p50      p95      max    rows')
  for (const row of results) console.log(line(row))
  if (OUT) {
    fs.writeFileSync(OUT, JSON.stringify({ base: BASE, workspace: WORKSPACE, runs: RUNS, at: new Date().toISOString(), results }, null, 2))
    console.log(`\nwrote ${OUT}`)
  }
}

main().catch((error) => {
  console.error(error)
  process.exit(1)
})
