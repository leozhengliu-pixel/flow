import { useSyncExternalStore } from 'react'
import type { User, UserSettings } from '@/types/flow'

let current: Partial<UserSettings> = {}
const listeners = new Set<() => void>()
export function setRuntimePreferences(next: Partial<UserSettings>) {
  if (current === next) return
  current = next
  for (const listener of listeners) listener()
}
export function useUserPreferences() { return useSyncExternalStore(listener=>{listeners.add(listener);return()=>listeners.delete(listener)},()=>current,()=>current) }
export function commentShortcutMatches(event: {key:string;shiftKey:boolean;ctrlKey:boolean;metaKey:boolean;altKey:boolean;nativeEvent?:{isComposing?:boolean}}) {
  if (event.nativeEvent?.isComposing || event.key !== 'Enter' || event.shiftKey || event.altKey) return false
  return current.sendComments === '⌘ Enter' ? event.metaKey || event.ctrlKey : !event.metaKey && !event.ctrlKey
}
export function transformTextEmoticons(text:string) {
  if (!current.emoticons) return text
  const map: Record<string,string> = {':)':'🙂',':-)':'🙂',':(':'🙁',':-(':'🙁',';)':'😉',';-)':'😉',':D':'😃','<3':'❤️'}
  return text.replace(/(^|\s)(:-?\)|:-?\(|;-?\)|:D|<3)(?=\s|$)/g,(_,space,emoticon)=>space+map[emoticon])
}
export function displayUserName(user: Pick<User,'displayName'|'name'>) {
  const full = user.displayName || user.name
  if (current.displayNames === 'Username') return user.name || full
  if (current.displayNames === 'First name') return full.split(/\s+/)[0]
  return full
}
export function firstWeekday(preference = current.firstDay) { return preference === 'Sunday' ? 0 : preference === 'Saturday' ? 6 : 1 }
