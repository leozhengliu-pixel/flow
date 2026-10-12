import { createContext, useContext } from 'react'
import type { ApplicationTask } from '@/lib/application-agents'

export type Directory = { key: string; tasks: ApplicationTask[]; error: string; refresh: () => void; onUpload?: (file: File) => Promise<string> }
export const TaskDirectory = createContext<Directory | null>(null)
export const CommentThreads = createContext<ReadonlySet<string>>(new Set())
export function useAgentCommentThreadIds() { return useContext(CommentThreads) }
