// API helpers for the clips' `prepare` step: put the demo data back into the
// state a clip expects, so every clip can be re-recorded any number of times.
import { DOCUMENTS } from './content.mjs'
import { Client, WORKSPACE, readState } from './api.mjs'

// The project directory reports issueCount 0 / progress 0% for workspaces whose
// issues live in the issue-record index, so the list clips hide those columns.
export const projectsListStorage = {
  [`flow:projects:view:${WORKSPACE}:workspace:all`]: {
    grouping: 'Status',
    groupOrder: 'asc',
    layout: 'list',
    onlyLeadTeamProjects: false,
    ordering: 'Name',
    orderingDirection: 'asc',
    properties: ['Milestones', 'Summary', 'Priority', 'Health', 'Lead', 'Target date'],
    showClosed: 'All',
    showEmptyGroups: false,
    subGrouping: 'No grouping',
  },
}

export async function connectDemo() {
  const cookies = readState('cookies.json')
  if (!cookies?.maya) throw new Error('No demo session: run `node scripts/feature-tour/seed.mjs` first')
  const clients = Object.fromEntries(Object.entries(cookies).map(([key, cookie]) => [key, new Client(cookie)]))
  const maya = clients.maya
  const demo = {
    maya,
    clients,
    async boot() {
      return maya.get('/api/bootstrap')
    },
    async issue(title) {
      const boot = await demo.boot()
      const issue = boot.issues.find((candidate) => candidate.title === title)
      if (!issue) throw new Error(`demo issue not found: ${title}`)
      return issue
    },
    async project(name) {
      const boot = await demo.boot()
      const project = boot.projects.find((candidate) => candidate.name === name)
      if (!project) throw new Error(`demo project not found: ${name}`)
      return project
    },
    async patchIssue(title, patch) {
      const issue = await demo.issue(title)
      return maya.patch(`/api/issues/${issue.id}`, patch)
    },
    // Deletes saved views by name (all of them when no names are given).
    async document(title) {
      const boot = await demo.boot()
      const document = boot.documents.find((candidate) => candidate.title === title)
      if (!document) throw new Error(`demo document not found: ${title}`)
      return document
    },
    // Puts a seeded document back to its original state by recreating it (the
    // editor keeps live collaborative state, so patching the markdown is not enough).
    async restoreDocument(title) {
      const document = await demo.document(title)
      const full = await maya.get(`/api/documents/${document.id}`)
      await maya.delete(`/api/documents/${document.id}`)
      await demo.emptyTrash()
      return maya.post('/api/documents', { title, teamIds: full.teamIds, projectIds: full.projectIds, icon: full.icon, content: DOCUMENTS[title] })
    },
    // Removes every agent chat so each take starts from an empty history.
    async clearAgentSessions() {
      const sessions = await maya.get('/api/agent/sessions')
      for (const session of sessions.nodes ?? sessions.sessions ?? sessions) await maya.delete(`/api/agent/sessions/${session.id}`).catch(() => {})
    },
    // Remembers the current issues; purgeIssuesAfter(snapshot) deletes anything created since (e.g. by the agent).
    async snapshotIssues() {
      return new Set((await demo.boot()).issues.map((issue) => issue.id))
    },
    async purgeIssuesAfter(snapshot) {
      const boot = await demo.boot()
      for (const issue of boot.issues.filter((candidate) => !snapshot.has(candidate.id))) await maya.delete(`/api/issues/${issue.id}`).catch(() => {})
      await demo.emptyTrash()
    },
    async commentIds(issueTitle) {
      const issue = await demo.issue(issueTitle)
      return { issue, ids: new Set(((await demo.boot()).comments?.[issue.id] ?? []).map((comment) => comment.id)) }
    },
    async purgeCommentsAfter({ issue, ids }) {
      const comments = (await demo.boot()).comments?.[issue.id] ?? []
      for (const comment of comments.filter((item) => !ids.has(item.id) && !item.parentId)) await maya.delete(`/api/issues/${issue.id}/comments/${comment.id}`).catch(() => {})
      for (const comment of comments.filter((item) => !ids.has(item.id) && item.parentId)) await maya.delete(`/api/issues/${issue.id}/comments/${comment.id}`).catch(() => {})
    },
    async deleteViews(names) {
      const boot = await demo.boot()
      for (const view of boot.savedViews || []) if (!names || names.includes(view.name)) await maya.delete(`/api/views/${view.id}`).catch(() => {})
      await demo.emptyTrash()
    },
    // Marks every notification unread and un-snoozed again.
    async resetInbox() {
      const boot = await demo.boot()
      for (const notification of boot.notifications || []) {
        // Notifications the agent clips produced are not part of the seeded inbox.
        if (notification.type === 'agentReply') {
          await maya.patch(`/api/notifications/${notification.id}`, { deleted: true }).catch(() => {})
          continue
        }
        await maya.patch(`/api/notifications/${notification.id}`, { read: false, archived: false, snoozedUntil: null }).catch(() => {})
      }
    },
    // Permanently removes everything created while recording a clip.
    async purgeIssues(titles) {
      const boot = await demo.boot()
      for (const issue of boot.issues.filter((candidate) => titles.includes(candidate.title))) await maya.delete(`/api/issues/${issue.id}`)
      await demo.emptyTrash()
    },
    async clearDrafts() {
      await maya.delete('/api/drafts').catch(() => {})
    },
    async emptyTrash() {
      const boot = await demo.boot()
      for (const entry of boot.trash || []) await maya.delete(`/api/trash/${entry.id}`).catch(() => {})
    },
  }
  return demo
}
