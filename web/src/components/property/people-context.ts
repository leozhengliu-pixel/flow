import { createContext, useContext } from 'react'
import type { User, WorkspaceMember } from '@/types/flow'

export const PeopleContext = createContext<{ users: ReadonlyMap<string, User>; members: ReadonlyMap<string, WorkspaceMember>; teams: ReadonlyMap<string, string[]>; teamIds: ReadonlyMap<string, string[]>; projects: ReadonlyMap<string, string[]>; workspaceName?: string }>({ users: new Map(), members: new Map(), teams: new Map(), teamIds: new Map(), projects: new Map() })
export function usePeopleDirectory() { return useContext(PeopleContext) }
