import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import type { Issue, User } from '@/types/flow'
import type { WorkspaceStore } from '@/store/workspace-store'
import { personDisplayName } from '@/lib/people'

export type HydratedMention = {
  id: string
  label: string
  title?: string
  href: string
  mentionType: 'user' | 'issue' | 'entity'
}

export type MentionHydrationResolver = (url: string) => HydratedMention | null | Promise<HydratedMention | null>

const mentionHydrationKey = new PluginKey('flowMentionHydration')

/** Flow / Linear-style issue path: /{workspace}/issue/{IDENTIFIER}/... */
const ISSUE_PATH = /(?:^|\/)(?:[\w.-]+)\/issue\/([A-Za-z]+-\d+)(?:\/|$|\?|#)/i
/** Optional people path: /{workspace}/profiles/{userId} or /settings/members ... */
const USER_PATH = /(?:^|\/)(?:[\w.-]+)\/(?:profiles|member)\/([\w.-]+)(?:\/|$|\?|#)/i

/**
 * Resolve a pasted URL against the workspace store (LS-0407).
 * Synchronous directory lookup only — REST hydrate reserved for P2.
 */
export function resolveMentionFromStore(url: string, store: WorkspaceStore | null | undefined): HydratedMention | null {
  if (!store?.data || !url) return null
  let parsed: URL
  try {
    parsed = new URL(url, typeof window !== 'undefined' ? window.location.origin : 'https://flow.local')
  } catch {
    return null
  }
  const path = `${parsed.pathname}${parsed.search}`
  const issueMatch = path.match(ISSUE_PATH)
  if (issueMatch) {
    const identifier = issueMatch[1].toUpperCase()
    for (const issue of store.entities.issue.values()) {
      if (issue.identifier.toUpperCase() === identifier) {
        return mentionFromIssue(issue, parsed.href)
      }
    }
    for (const issue of store.data.issues ?? []) {
      if (issue.identifier.toUpperCase() === identifier) {
        return mentionFromIssue(issue, parsed.href)
      }
    }
  }
  const userMatch = path.match(USER_PATH)
  if (userMatch) {
    const user = store.getById('user', userMatch[1]) as User | undefined
    if (user) return mentionFromUser(user, parsed.href)
  }
  return null
}

function mentionFromIssue(issue: Pick<Issue, 'id' | 'identifier' | 'title'>, href: string): HydratedMention {
  return {
    id: issue.id,
    label: issue.identifier,
    title: issue.title,
    href,
    mentionType: 'issue',
  }
}

function mentionFromUser(user: User, href: string): HydratedMention {
  return {
    id: user.id,
    label: personDisplayName(user) || user.name || user.id,
    href,
    mentionType: 'user',
  }
}

export function extractUrlsFromClipboard(text: string): string[] {
  const matches = text.match(/https?:\/\/[^\s<>"']+/gi) ?? []
  const relative = text.match(/(?:^|\s)(\/[^\s<>"']+)/g)?.map((part) => part.trim()) ?? []
  return [...matches, ...relative].map((value) => value.replace(/[),.;]+$/, ''))
}

/**
 * TipTap plugin: paste Flow/Linear entity URLs → mention atoms with store metadata.
 */
export const MentionHydrationPlugin = Extension.create<{
  resolveFromUrl?: MentionHydrationResolver
}>({
  name: 'mentionHydration',

  addOptions() {
    return {
      resolveFromUrl: undefined,
    }
  },

  addProseMirrorPlugins() {
    const resolveFromUrl = this.options.resolveFromUrl
    const mentionType = this.editor.schema.nodes.mention
    if (!resolveFromUrl || !mentionType) return []

    return [
      new Plugin({
        key: mentionHydrationKey,
        props: {
          handlePaste: (view, event) => {
            const text = event.clipboardData?.getData('text/plain')?.trim()
            if (!text) return false
            const urls = extractUrlsFromClipboard(text)
            if (!urls.length) return false
            // Only convert when clipboard is primarily a URL (or URL-only paste).
            const plainWithoutUrls = urls.reduce((acc, url) => acc.replace(url, ''), text).trim()
            if (plainWithoutUrls.length > 0 && urls.length === 1 && text !== urls[0]) {
              return false
            }

            const pending = urls.map((url) => ({ url, resolved: resolveFromUrl(url) }))

            void (async () => {
              const nodes = []
              for (const item of pending) {
                const resolved = await item.resolved
                if (!resolved) continue
                nodes.push(
                  mentionType.create({
                    id: resolved.id,
                    label: resolved.label,
                    title: resolved.title ?? '',
                    href: resolved.href,
                    mentionType: resolved.mentionType,
                  }),
                )
              }
              if (!nodes.length) return
              const tr = view.state.tr.replaceSelectionWith(nodes[0], false)
              for (let index = 1; index < nodes.length; index += 1) {
                tr.insert(tr.selection.to, nodes[index])
              }
              view.dispatch(tr.scrollIntoView())
            })()

            // If the clipboard is a single resolvable URL, claim the paste.
            if (urls.length === 1 && plainWithoutUrls.length === 0) {
              event.preventDefault()
              return true
            }
            return false
          },
        },
      }),
    ]
  },
})

/** Convenience factory wired to a live WorkspaceStore getter. */
export function createMentionHydrationExtension(getStore: () => WorkspaceStore | null | undefined) {
  return MentionHydrationPlugin.configure({
    resolveFromUrl: (url) => resolveMentionFromStore(url, getStore()),
  })
}
