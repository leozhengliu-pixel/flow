import { useMemo } from 'react'
import type { Comment } from '@/types/flow'
import { buildThreads } from './inline-comments-model'

export function useInlineThreads(comments: Comment[]) {
  return useMemo(() => {
    const threads = buildThreads(comments)
    return { threads, open: threads.filter(thread => !thread.root.resolved), resolved: threads.filter(thread => thread.root.resolved) }
  }, [comments])
}
