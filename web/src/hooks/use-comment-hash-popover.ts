import { useEffect, useState } from 'react'
import { activityTargetFromHash } from '@/components/activity/activity-highlight'

export function isCommentIdInHash(hash: string, commentId: string) {
  const target = activityTargetFromHash(hash)
  return target?.kind === 'comment' && target.id === commentId
}

export function isAnyCommentInHash(hash = typeof window !== 'undefined' ? window.location.hash : '') {
  return activityTargetFromHash(hash)?.kind === 'comment'
}

export type CommentHashTarget = { id: string; key: string }

/** LS-0382 — hash `#comment-{id}` → CommentPopover host target. */
export function useCommentHashPopover(enabled = true) {
  const [target, setTarget] = useState<CommentHashTarget | undefined>(() => readCommentHash())

  useEffect(() => {
    if (!enabled) {
      setTarget(undefined)
      return
    }
    const sync = () => setTarget(readCommentHash())
    sync()
    window.addEventListener('hashchange', sync)
    window.addEventListener('popstate', sync)
    return () => {
      window.removeEventListener('hashchange', sync)
      window.removeEventListener('popstate', sync)
    }
  }, [enabled])

  return {
    target,
    isAnyCommentInHash: Boolean(target),
    clear: () => {
      if (typeof window === 'undefined') return
      const { pathname, search } = window.location
      window.history.replaceState(null, '', `${pathname}${search}`)
      setTarget(undefined)
    },
  }
}

function readCommentHash(): CommentHashTarget | undefined {
  if (typeof window === 'undefined') return undefined
  const parsed = activityTargetFromHash(window.location.hash)
  if (parsed?.kind !== 'comment') return undefined
  return { id: parsed.id, key: parsed.key }
}
