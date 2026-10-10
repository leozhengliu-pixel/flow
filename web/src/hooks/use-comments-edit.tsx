import type { ReactNode } from 'react'
import { CommentsEditContext, useCommentsEditState } from './use-comments-edit-state'

export function CommentsEditProvider({ children }: { children: ReactNode }) {
  const value = useCommentsEditState()
  return <CommentsEditContext.Provider value={value}>{children}</CommentsEditContext.Provider>
}
