import { createContext, useContext } from 'react'
import type { User, WorkspaceMember } from '@/types/flow'

/** What a team's hover card shows, plus where the team sits in the hierarchy. */
export interface TeamDirectoryEntry { parentId?: string; memberIds: string[]; projectCount: number; documentCount: number }
const NO_TEAMS: ReadonlyMap<string, TeamDirectoryEntry> = new Map()
export const PeopleContext = createContext<{ teamDirectory: ReadonlyMap<string, TeamDirectoryEntry>; users: ReadonlyMap<string, User>; members: ReadonlyMap<string, WorkspaceMember>; teams: ReadonlyMap<string, string[]>; teamIds: ReadonlyMap<string, string[]>; projects: ReadonlyMap<string, string[]>; workspaceName?: string }>({ teamDirectory: NO_TEAMS, users: new Map(), members: new Map(), teams: new Map(), teamIds: new Map(), projects: new Map() })
export function usePeopleDirectory() { return useContext(PeopleContext) }
