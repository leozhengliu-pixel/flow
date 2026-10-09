import { useEffect, useMemo, useState } from 'react'
import { peekAgentRecord, settleAgentRecords } from '@/components/agent/agent-entity-fetch'
import { useAgentEntityData } from '@/components/agent/agent-entity-data'
import { isAgentIdentifier } from '@/components/agent/agent-entity-refs'
import { listIssueRecords } from '@/lib/api'
import type { Issue, User } from '@/types/flow'
import { mentionOptions, type MentionOption } from './mention-options'

const TYPED_IDENTIFIER = /^[A-Za-z][A-Za-z0-9]*-\d+$/

/**
 * The "@" menu's options for the text typed after the "@". Paged workspaces never hold every issue, so the menu asks the
 * server for matching (or, with nothing typed, recent) issues while it is open, and fetches a typed identifier by id.
 */
export function useMentionOptions({ active, query, users }: { active: boolean; query: string; users: User[] }): MentionOption[] {
  const data = useAgentEntityData()
  const workspace = data?.workspace.urlKey
  const [fetched, setFetched] = useState<Issue[]>([])
  useEffect(() => {
    if (!active || !workspace) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      listIssueRecords({ ...(query.trim() ? { q: query.trim() } : { sort: 'updatedAt', direction: 'desc' }), archived: 'false', limit: query.trim() ? 8 : 50 }, controller.signal, workspace)
        .then(page => setFetched(page.items))
        .catch(() => undefined)
    }, query.trim() ? 150 : 0)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [active, query, workspace])
  const [byIdentifier, setByIdentifier] = useState<Issue>()
  useEffect(() => {
    setByIdentifier(undefined)
    const identifier = query.trim()
    if (!active || !data || !TYPED_IDENTIFIER.test(identifier) || !isAgentIdentifier(data, identifier)) return
    let cancelled = false
    void settleAgentRecords(data.workspace.urlKey, [{ kind: 'issue', id: identifier.toUpperCase() }]).then(() => {
      const record = peekAgentRecord(data.workspace.urlKey, 'issue', identifier.toUpperCase())
      if (!cancelled && record) setByIdentifier(record as Issue)
    })
    return () => { cancelled = true }
  }, [active, data, query])
  return useMemo(() => {
    if (!active) return []
    const pool = new Map<string, Issue>()
    for (const issue of [...(data?.issues ?? []), ...fetched, ...(byIdentifier ? [byIdentifier] : [])]) pool.set(issue.id, issue)
    return mentionOptions(data, query, { users, issues: [...pool.values()] })
  }, [active, byIdentifier, data, fetched, query, users])
}
