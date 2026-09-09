import { updateUserSettings } from './api'
import type { UserSettings } from '@/types/flow'

const pending = new Map<string, Promise<UserSettings>>()

// The queue belongs to the workspace/user, not the Settings component, so
// navigating away cannot cancel edits or send them to a different workspace.
export function persistUserSettings(workspaceKey: string, userId: string, patch: Partial<UserSettings>): Promise<UserSettings> {
  const key = `${workspaceKey}:${userId}`
  const previous = pending.get(key)
  const write = () => updateUserSettings(patch, workspaceKey)
  const result = previous ? previous.then(write, write) : write()
  pending.set(key,result)
  void result.then(() => {if(pending.get(key) === result) pending.delete(key)},() => {if(pending.get(key) === result) pending.delete(key)})
  return result
}
