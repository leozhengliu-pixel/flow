#!/usr/bin/env node
// Seeds a clean, invented "Acme" workspace for the feature tour recordings.
//
//   node scripts/feature-tour/seed.mjs
//
// Needs the API (:8080, started with FLOW_DEV_AUTH_TOKENS=true so that the
// email-verification token is returned) and the web dev server (:5173) to be
// running. Everything goes through the public HTTP API; nothing touches the
// database. Safe to re-run: accounts, workspace, teams and members are
// "ensured", and the content (labels, issues, projects, ...) is only created
// when the workspace has no issues yet. Credentials of the demo accounts live
// in scripts/feature-tour/.state (git-ignored).
import crypto from 'node:crypto'
import { DOCUMENTS } from './lib/content.mjs'
import { BASE, Client, PEOPLE, WORKSPACE, emailOf, readCredentials, readState, writeState } from './lib/api.mjs'

const log = (message) => console.log(`[seed] ${message}`)

// -- accounts ---------------------------------------------------------------

let credentials = readCredentials()
if (!credentials) {
  credentials = { password: `Tour-${crypto.randomBytes(9).toString('base64url')}` }
  writeState('credentials.json', credentials)
}
const cookies = readState('cookies.json') || {}
const clients = {}

async function ensureAccount(person) {
  const email = emailOf(person.key)
  const cached = new Client(cookies[person.key] || '')
  if (cookies[person.key]) {
    const session = await cached.request('GET', '/api/auth/session', undefined, { raw: true })
    if (session.status === 200 && session.data?.user?.email === email) {
      clients[person.key] = cached
      return cached
    }
  }
  const anonymous = new Client()
  const registered = await anonymous.request('POST', '/api/auth/register', { name: person.name, email, password: credentials.password }, { raw: true })
  if (registered.status === 201) {
    const token = registered.data?.verificationToken
    if (!token) throw new Error('The API did not return a verification token; start it with FLOW_DEV_AUTH_TOKENS=true')
    await anonymous.post('/api/auth/verify-email', { token })
    log(`registered ${email}`)
  } else if (registered.status === 429) {
    throw new Error(`register is rate limited (5 per 15 minutes); wait and re-run (${email})`)
  } else if (registered.status !== 409) {
    throw new Error(`register ${email}: ${registered.status} ${JSON.stringify(registered.data)}`)
  }
  const client = new Client(cookies[person.key] || '')
  const session = await client.request('GET', '/api/auth/session', undefined, { raw: true })
  if (session.status !== 200 || session.data?.user?.email !== email) {
    const login = await client.request('POST', '/api/auth/login', { email, password: credentials.password }, { raw: true })
    if (login.status !== 200) throw new Error(`login ${email}: ${login.status} ${JSON.stringify(login.data)}`)
    cookies[person.key] = client.cookie
    writeState('cookies.json', cookies)
  }
  clients[person.key] = client
  return client
}

for (const person of PEOPLE) await ensureAccount(person)
const maya = clients.maya

// -- workspace, teams, members ---------------------------------------------

const memberships = (await maya.get('/api/auth/session')).memberships || []
if (!memberships.some((m) => (m.workspace?.urlKey || m.urlKey) === WORKSPACE)) {
  await maya.post('/api/workspaces', { name: 'Acme', urlKey: WORKSPACE, region: 'us' })
  log('created workspace')
}

let boot = await maya.get('/api/bootstrap')
let teams = boot.teams
const teamBy = (key) => teams.find((team) => team.key === key)
if (teamBy('ACM')) {
  await maya.patch(`/api/workspaces/${WORKSPACE}/teams/${teamBy('ACM').id}`, { name: 'Web', key: 'WEB', color: '#4EA7FC' })
}
for (const [name, key, color] of [
  ['Mobile', 'MOB', '#F2994A'],
  ['Platform', 'PLT', '#26B5CE'],
]) {
  if (!teams.some((team) => team.key === key)) await maya.post(`/api/workspaces/${WORKSPACE}/teams`, { name, key, color })
}
boot = await maya.get('/api/bootstrap')
teams = boot.teams
const WEB = teamBy('WEB').id
const MOB = teamBy('MOB').id
const PLT = teamBy('PLT').id

const users = (await maya.get('/api/users')).nodes
const userId = (key) => users.find((user) => user.email === emailOf(key))?.id
const missing = PEOPLE.filter((person) => person.key !== 'maya' && !userId(person.key))
if (missing.length) {
  const invitations = await maya.post(`/api/workspaces/${WORKSPACE}/invitations`, { emails: missing.map((person) => emailOf(person.key)), role: 'member' })
  for (const invitation of invitations) {
    const person = PEOPLE.find((candidate) => emailOf(candidate.key) === invitation.email)
    await clients[person.key].post('/api/invitations/accept', { token: invitation.token })
  }
  log(`added ${missing.length} members`)
}
const people = (await maya.get('/api/users')).nodes
const U = Object.fromEntries(PEOPLE.map((person) => [person.key, people.find((user) => user.email === emailOf(person.key)).id]))

const teamMembers = { [WEB]: ['daniel', 'priya', 'lucas', 'sofia'], [MOB]: ['sofia', 'lucas', 'daniel'], [PLT]: ['priya', 'daniel', 'lucas'] }
for (const [teamId, keys] of Object.entries(teamMembers)) {
  for (const key of keys) await maya.put(`/api/workspaces/${WORKSPACE}/teams/${teamId}/members/${U[key]}`, { member: true }).catch(() => {})
}

// A member only becomes assignable (issues, project leads) once the workspace
// has seen them act, so every teammate favorites their team once.
for (const key of teamMembers[WEB]) {
  await clients[key].put(`/api/favorites/team/${WEB}`, {}).catch(() => {})
}

for (const teamId of [WEB, MOB, PLT]) await maya.patch(`/api/teams/${teamId}/settings`, { estimateType: 'fibonacci' })
if (!(await maya.get('/api/cycles')).nodes.length) {
  await maya.patch(`/api/teams/${WEB}/cycle-settings`, { enabled: true, durationWeeks: 2, upcomingCount: 1, capacity: 6, autoCreate: true, autoMigrate: true })
}

// Account preferences used for every recording: English, dark.
await maya.patch('/api/account/settings', { language: 'en', interfaceTheme: 'Dark' })

if ((await maya.get('/api/issue-records?limit=1')).items?.length) {
  log('workspace already has issues; content seeding skipped')
  process.exit(0)
}

// -- content ------------------------------------------------------------------

const day = (offset) => new Date(Date.now() + offset * 86_400_000).toISOString().slice(0, 10)
const labelByName = {}
const existingLabels = await maya.get('/api/labels')
for (const label of existingLabels.nodes ?? existingLabels) labelByName[label.name] = label.id
for (const [name, color] of [
  ['Design', '#BB87FC'],
  ['Performance', '#F2994A'],
  ['Accessibility', '#4EA7FC'],
  ['Security', '#EB5757'],
  ['Docs', '#26B5CE'],
]) {
  if (!labelByName[name]) labelByName[name] = (await maya.post('/api/labels', { name, color })).id
}

const cycle = (await maya.get('/api/cycles')).nodes.find((item) => item.status === 'current')
const STATE = { backlog: 'state_backlog', todo: 'state_todo', progress: 'state_progress', done: 'state_done' }
const PRI = { urgent: 1, high: 2, medium: 3, low: 4 }

async function project(spec) {
  const created = await maya.post('/api/projects', {
    name: spec.name,
    summary: spec.summary,
    description: spec.description,
    icon: spec.icon,
    color: spec.color,
    statusId: spec.statusId,
    priority: PRI[spec.priority],
    leadId: U[spec.lead],
    memberIds: spec.members.map((key) => U[key]),
    teamIds: spec.teams,
    startDate: day(spec.start),
    targetDate: day(spec.target),
  })
  const milestones = {}
  for (const [name, offset] of spec.milestones) {
    const milestone = await maya.post(`/api/projects/${created.id}/milestones`, { name, targetDate: day(offset), description: '' })
    milestones[name] = milestone.id
  }
  return { id: created.id, milestones }
}

const checkout = await project({
  name: 'Checkout redesign',
  summary: 'A faster, clearer checkout with wallets and upfront taxes.',
  description: 'Replace the three-step checkout with a single page that shows taxes and shipping up front and supports Apple Pay and Google Pay.',
  icon: 'Cart',
  color: '#4EA7FC',
  statusId: 'ps_progress',
  priority: 'high',
  lead: 'priya',
  members: ['priya', 'lucas', 'daniel', 'sofia'],
  teams: [WEB],
  start: -14,
  target: 38,
  milestones: [['Design sign-off', 10], ['Private beta', 24], ['General availability', 38]],
})
const mobile = await project({
  name: 'Mobile app 3.0',
  summary: 'Offline mode, a new tab bar and faster startup.',
  description: 'The 3.0 release rebuilds navigation, adds offline editing with background sync and brings cold start under 1.5 seconds.',
  icon: 'Phone',
  color: '#F2994A',
  statusId: 'ps_progress',
  priority: 'urgent',
  lead: 'sofia',
  members: ['sofia', 'lucas', 'daniel'],
  teams: [MOB],
  start: -21,
  target: 52,
  milestones: [['Navigation rework', 14], ['Offline mode', 31], ['Public beta', 52]],
})
const limiter = await project({
  name: 'API rate limiting',
  summary: 'Protect the public API with per-workspace limits.',
  description: 'Introduce token-bucket rate limiting, response headers and an admin override, rolled out in shadow mode first.',
  icon: 'Shield',
  color: '#26B5CE',
  statusId: 'ps_progress',
  priority: 'high',
  lead: 'daniel',
  members: ['daniel', 'priya'],
  teams: [PLT],
  start: -10,
  target: 45,
  milestones: [['Design review', 7], ['Shadow mode', 24], ['Enforcement', 45]],
})
const onboarding = await project({
  name: 'Onboarding revamp',
  summary: 'Get new teams to their first issue in under three minutes.',
  description: 'A welcome checklist, a single-step signup and a guided first project for every new workspace.',
  icon: 'Rocket',
  color: '#BB87FC',
  statusId: 'ps_progress',
  priority: 'medium',
  lead: 'lucas',
  members: ['lucas', 'priya', 'sofia', 'maya'],
  teams: [WEB, MOB],
  start: -5,
  target: 60,
  milestones: [['Welcome checklist', 20], ['Single-step signup', 40], ['Guided first project', 60]],
})

const issueIds = {}
async function issue(team, spec) {
  const created = await maya.post('/api/issues', {
    teamId: team,
    title: spec.title,
    description: spec.description || '',
    stateId: STATE[spec.state],
    priority: PRI[spec.priority],
    estimate: spec.estimate,
    assigneeId: spec.assignee ? U[spec.assignee] : undefined,
    labelIds: (spec.labels || []).map((name) => labelByName[name]),
    projectId: spec.project?.id,
    projectMilestoneId: spec.project && spec.milestone ? spec.project.milestones[spec.milestone] : undefined,
    cycleId: spec.cycle ? cycle.id : undefined,
    parentId: spec.parent,
    dueDate: spec.due === undefined ? undefined : day(spec.due),
  })
  issueIds[created.identifier] = created.id
  return created
}

// Web
const cart = await issue(WEB, {
  title: 'Redesign cart summary with itemized taxes',
  description: 'Show subtotal, shipping and taxes line by line so customers know the final price before they reach payment.\n\n- Itemized tax rows per jurisdiction\n- Shipping estimate updates as the address changes\n- Empty, loading and error states',
  state: 'progress', priority: 'high', estimate: 5, assignee: 'priya', labels: ['Feature'], project: checkout, milestone: 'Design sign-off', cycle: true, due: 6,
})
await issue(WEB, { title: 'Design cart summary states (empty, loading, error)', state: 'done', priority: 'medium', estimate: 2, assignee: 'lucas', labels: ['Design'], project: checkout, milestone: 'Design sign-off', cycle: true, parent: cart.id })
await issue(WEB, { title: 'Implement tax breakdown component', state: 'progress', priority: 'high', estimate: 3, assignee: 'priya', labels: ['Feature'], project: checkout, milestone: 'Design sign-off', cycle: true, parent: cart.id })
await issue(WEB, { title: 'Add unit tests for tax rounding', state: 'todo', priority: 'medium', estimate: 2, assignee: 'daniel', labels: ['Improvement'], project: checkout, milestone: 'Design sign-off', cycle: true, parent: cart.id })
await issue(WEB, { title: 'Support Apple Pay and Google Pay at checkout', description: 'Offer wallet payments as the first option on mobile browsers and on Safari desktop.', state: 'todo', priority: 'high', estimate: 8, assignee: 'daniel', labels: ['Feature'], project: checkout, milestone: 'Private beta', cycle: true, due: 20 })
const focusBug = await issue(WEB, {
  title: 'Address form loses focus after a validation error',
  description: 'After submitting an invalid postcode the form re-renders and the cursor jumps out of the field.\n\n**Steps to reproduce**\n1. Open checkout and fill in the address\n2. Enter an invalid postcode and press Tab\n3. Try to type the correct value\n\n**Expected:** focus stays on the field with the error.',
  state: 'progress', priority: 'urgent', estimate: 2, assignee: 'priya', labels: ['Bug'], project: checkout, milestone: 'Design sign-off', cycle: true, due: 3,
})
await issue(WEB, { title: 'Checkout page LCP over 4s on slow 3G', description: 'The hero image and the payment SDK block the first render. Defer the SDK and serve responsive images.', state: 'todo', priority: 'medium', estimate: 5, assignee: 'lucas', labels: ['Performance'], project: checkout, milestone: 'Private beta', cycle: true })
await issue(WEB, { title: 'Promo code field accepts trailing whitespace', state: 'todo', priority: 'low', estimate: 1, assignee: 'sofia', labels: ['Bug'], project: checkout })
const guest = await issue(WEB, { title: 'Guest checkout: skip account creation', description: 'Let first-time buyers pay without creating an account and offer to save their details afterwards.', state: 'backlog', priority: 'medium', estimate: 8, assignee: 'maya', labels: ['Feature'], project: checkout, milestone: 'General availability' })
await issue(WEB, { title: 'Keyboard navigation for the product gallery', state: 'todo', priority: 'medium', estimate: 3, assignee: 'lucas', labels: ['Accessibility'], cycle: true })
await issue(WEB, { title: 'Dark mode: invoice PDF has unreadable table borders', state: 'done', priority: 'medium', estimate: 2, assignee: 'sofia', labels: ['Bug'] })
await issue(WEB, { title: 'Migrate marketing pages to the new design tokens', state: 'progress', priority: 'medium', estimate: 5, assignee: 'lucas', labels: ['Improvement', 'Design'] })
await issue(WEB, { title: 'Welcome checklist on first login', description: 'Show a short checklist (create an issue, invite a teammate, connect GitHub) on the first login and track completion.', state: 'progress', priority: 'high', estimate: 5, assignee: 'lucas', labels: ['Feature'], project: onboarding, milestone: 'Welcome checklist', cycle: true })
await issue(WEB, { title: 'Replace the signup wizard with a single-step form', state: 'todo', priority: 'medium', estimate: 5, assignee: 'priya', labels: ['Feature'], project: onboarding, milestone: 'Single-step signup' })
await issue(WEB, { title: 'Invite step is skipped for SSO users', state: 'todo', priority: 'high', estimate: 2, assignee: 'sofia', labels: ['Bug'], project: onboarding, milestone: 'Welcome checklist' })
await issue(WEB, { title: 'Write the onboarding analytics events spec', state: 'done', priority: 'low', estimate: 2, assignee: 'maya', labels: ['Docs'], project: onboarding, milestone: 'Welcome checklist' })

// Mobile
await issue(MOB, { title: 'Offline mode: queue edits and sync on reconnect', description: 'Edits made without a connection are stored locally and replayed in order. Conflicts surface in the issue activity.', state: 'progress', priority: 'urgent', estimate: 8, assignee: 'sofia', labels: ['Feature'], project: mobile, milestone: 'Offline mode', due: 12 })
await issue(MOB, { title: 'Bottom tab bar redesign', state: 'done', priority: 'high', estimate: 5, assignee: 'lucas', labels: ['Design', 'Feature'], project: mobile, milestone: 'Navigation rework' })
await issue(MOB, { title: 'Crash on launch when notification permission is denied (iOS 18)', state: 'todo', priority: 'urgent', estimate: 3, assignee: 'daniel', labels: ['Bug'], project: mobile })
await issue(MOB, { title: 'Push notification deep links open the wrong screen', state: 'progress', priority: 'high', estimate: 3, assignee: 'sofia', labels: ['Bug'], project: mobile, milestone: 'Navigation rework' })
await issue(MOB, { title: 'Reduce cold start time below 1.5s', state: 'todo', priority: 'medium', estimate: 5, assignee: 'daniel', labels: ['Performance'], project: mobile })
await issue(MOB, { title: 'Support Dynamic Type in issue lists', state: 'backlog', priority: 'medium', estimate: 3, labels: ['Accessibility'] })
await issue(MOB, { title: 'Biometric unlock for returning users', state: 'backlog', priority: 'medium', estimate: 5, assignee: 'sofia', labels: ['Feature', 'Security'], project: mobile, milestone: 'Public beta' })
await issue(MOB, { title: 'Swipe actions on list rows', state: 'todo', priority: 'low', estimate: 2, assignee: 'lucas', labels: ['Improvement'], project: mobile, milestone: 'Navigation rework' })
await issue(MOB, { title: 'Show app version and build number in Settings', state: 'done', priority: 'low', estimate: 1, assignee: 'daniel', labels: ['Improvement'] })

// Platform
await issue(PLT, { title: 'Token bucket limiter middleware', description: 'Per-workspace token buckets with a burst allowance. Keep the limiter behind a feature flag until shadow mode is validated.', state: 'progress', priority: 'high', estimate: 5, assignee: 'daniel', labels: ['Feature'], project: limiter, milestone: 'Shadow mode', due: 9 })
await issue(PLT, { title: 'Expose rate limit headers on every response', state: 'todo', priority: 'medium', estimate: 2, assignee: 'priya', labels: ['Feature'], project: limiter, milestone: 'Shadow mode' })
await issue(PLT, { title: 'Per-workspace limits and admin override', state: 'backlog', priority: 'medium', estimate: 5, labels: ['Feature'], project: limiter, milestone: 'Enforcement' })
await issue(PLT, { title: 'Dashboard for 429 responses by endpoint', state: 'todo', priority: 'medium', estimate: 3, assignee: 'daniel', labels: ['Improvement'], project: limiter, milestone: 'Shadow mode' })
const rateDocs = await issue(PLT, { title: 'Document the rate limit policy in the API reference', state: 'todo', priority: 'low', estimate: 2, assignee: 'maya', labels: ['Docs'], project: limiter, milestone: 'Enforcement' })
await issue(PLT, { title: 'Database connection pool exhausted during the nightly export', state: 'progress', priority: 'urgent', estimate: 3, assignee: 'priya', labels: ['Bug'] })
await issue(PLT, { title: 'Rotate API signing keys every 90 days', state: 'backlog', priority: 'high', estimate: 5, assignee: 'lucas', labels: ['Security'] })
await issue(PLT, { title: 'Upgrade Postgres to 16', state: 'backlog', priority: 'medium', estimate: 8, assignee: 'daniel', labels: ['Improvement'] })
await issue(PLT, { title: 'Add request tracing to background jobs', state: 'done', priority: 'medium', estimate: 3, assignee: 'priya', labels: ['Improvement'] })
log(`created ${Object.keys(issueIds).length} issues`)

// Conversations on the focus bug, with replies and reactions.
const dev = (key) => clients[key]
const first = await dev('daniel').post(`/api/issues/${focusBug.id}/comments`, { body: 'Reproduced on Safari 18 and Chrome. The blur handler re-renders the whole form when validation fails, so React remounts the input.' })
const reply = await dev('priya').post(`/api/issues/${focusBug.id}/comments`, { body: 'Good catch. I will key the fields by name so they keep focus across renders.', parentId: first.id })
await dev('maya').post(`/api/issues/${focusBug.id}/comments`, { body: 'Thanks both. Can we land this before the design sign-off on Friday?', parentId: first.id })
await dev('maya').post(`/api/issues/${focusBug.id}/comments/${reply.id}/reactions`, { emoji: '👍' })
await dev('daniel').post(`/api/issues/${focusBug.id}/comments/${reply.id}/reactions`, { emoji: '🎉' })
await dev('lucas').post(`/api/issues/${focusBug.id}/comments`, { body: 'Design note: please also move the error summary above the first invalid field so screen readers announce it first.' })

// Projects: updates (the onboarding project is left without one on purpose).
await maya.post(`/api/projects/${checkout.id}/updates`, { health: 'onTrack', body: '**Design sign-off is on track.** The itemized cart summary is in review and the address form bug is being fixed this week.\n\n- Wallet payments are estimated at two weeks\n- Beta customers are lined up for November' })
await maya.post(`/api/projects/${mobile.id}/updates`, { health: 'atRisk', body: '**Offline sync is the risk.** Conflict resolution needs another week of testing, which puts the public beta date under pressure.\n\n- Navigation rework is nearly done\n- Cold start time is down to 1.9s' })
await maya.post(`/api/projects/${limiter.id}/updates`, { health: 'onTrack', body: 'The token bucket middleware is merged behind a flag. Shadow mode starts next week and we will review the 429 dashboard together.' })

// Initiatives
const growth = await maya.post('/api/initiatives', {
  name: 'Grow self-serve revenue',
  summary: 'Make it effortless for new teams to start and pay.',
  description: 'Everything that moves visitors to paying customers without talking to sales: onboarding, checkout and pricing.',
  status: 'active',
  priority: 2,
  ownerId: U.maya,
  targetDate: day(75),
  icon: 'Rocket',
  color: '#BB87FC',
  projectIds: [checkout.id, onboarding.id],
})
const reliability = await maya.post('/api/initiatives', {
  name: 'Platform reliability',
  summary: 'Keep the API fast and predictable under load.',
  description: 'Rate limiting, connection management and better tracing for the services behind the product.',
  status: 'active',
  priority: 3,
  ownerId: U.daniel,
  targetDate: day(60),
  icon: 'Shield',
  color: '#26B5CE',
  projectIds: [limiter.id],
})
await maya.post(`/api/initiatives/${growth.id}/updates`, { health: 'onTrack', body: 'Checkout design is nearly signed off and the onboarding checklist entered development. We are on track for the December launch.' })
await maya.post(`/api/initiatives/${reliability.id}/updates`, { health: 'onTrack', body: 'Shadow mode for the rate limiter starts next week. Connection pool fixes are in review.' })

// Documents
const doc = (title, teamId, projectIds, icon, content) => maya.post('/api/documents', { title, teamIds: [teamId], projectIds, icon, content })
await doc('Checkout redesign: product spec', WEB, [checkout.id], 'Cart', DOCUMENTS["Checkout redesign: product spec"])
await doc('API rate limiting: design notes', PLT, [limiter.id], 'Shield', DOCUMENTS["API rate limiting: design notes"])
await doc('Onboarding research summary', WEB, [onboarding.id], 'Rocket', DOCUMENTS["Onboarding research summary"])

// Activity from teammates so the inbox has something to show.
await dev('daniel').post(`/api/issues/${rateDocs.id}/comments`, { body: '@Maya Chen the limits table in the design notes is final. Can you review the wording before we publish it?' })
await dev('priya').patch(`/api/issues/${guest.id}`, { stateId: STATE.todo })
await dev('sofia').post(`/api/issues/${guest.id}/comments`, { body: 'Do we still want to offer saving the details after payment? It affects the confirmation email.' })
await dev('lucas').patch(`/api/issues/${cart.id}`, { priority: PRI.urgent })

log(`done. Open ${BASE}/${WORKSPACE} (${emailOf('maya')}; password in scripts/feature-tour/.state/credentials.json)`)
