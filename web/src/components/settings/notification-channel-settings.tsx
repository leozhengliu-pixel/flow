import { ArrowLeft } from 'lucide-react'
import type { ReactNode } from 'react'
import type { NotificationCategory, NotificationChannelPreferences, NotificationPreferences } from '@/types/flow'
import { chatChannelPreferences } from './chat-personal-notifications'
import { SettingsPageTitle, SettingsSection, SettingsRow, SettingsToggle, SettingsSelect } from './settings-primitives'

const categories: [NotificationCategory,string,string][] = [
  ['assignments','Assignments','Assignments, unassignments, and membership changes'],
  ['statusChanges','Status changes','Changes to status, priority, and blocking relationships'],
  ['comments','Comments and replies','Comments, replies, and thread resolutions'],
  ['mentions','Mentions','Mentions in comments or content'],
  ['reactions','Reactions','Emoji reactions to your content'],
  ['subscriptions','Subscriptions','Activity on resources you subscribe to'],
  ['documents','Document changes','Changes to document content and subscriptions'],
  ['updates','Updates','Project and initiative updates'],
  ['reminders','Reminders and deadlines','Reminders, due dates, and SLA updates'],
  ['loops','Loops','Messages and failures from loops'],
  ['integrations','Apps and integrations','OAuth apps and integrations'],
  ['customerRequests','Customer requests','Requests from your customers'],
  ['triage','Triage','Issues added to triage'],
  ['pulse','Pulse summaries','Daily or weekly summaries of your subscribed project and initiative updates'],
]

/**
 * One delivery channel's settings: desktop, email, or the chat platform's
 * personal notifications (`chat`, direct messages from the Lark or Feishu app
 * bot; `name` is the platform's name, `notice` its current state, and
 * `locked` keeps it from being turned on until the account can receive them).
 */
export function NotificationChannelSettings({channel,preferences,save,onBack,p,name='',notice,locked=false}:{channel:'desktop'|'email'|'chat';preferences:NotificationPreferences;save:(value:NotificationPreferences)=>Promise<void>;onBack:()=>void;p:(text:string)=>string;name?:string;notice?:ReactNode;locked?:boolean}) {
  const value: NotificationChannelPreferences = channel === 'chat' ? chatChannelPreferences(preferences) : preferences[channel]
  const fill = (text: string) => text.replaceAll('{name}', name)
  const title = channel === 'chat' ? name : p(channel === 'desktop' ? 'Desktop' : 'Email')
  const description = channel === 'chat' ? fill(p('Direct messages from the {name} app bot to your linked account')) : p(channel === 'desktop' ? 'Applies across your browsers with notifications enabled' : 'Notifications delivered to your email address')
  const enableLabel = channel === 'chat' ? fill(p('Enable {name} notifications')) : p(`Enable ${channel} notifications`)
  const update = (next: NotificationChannelPreferences) => save({...preferences,[channel]:next})
  return <>
    <button className="settings-notification-back" type="button" onClick={onBack}><ArrowLeft size={14}/>{p('Notifications')}</button>
    <SettingsPageTitle description={description}>{title}</SettingsPageTitle>
    <SettingsSection title={p('Settings')}>
      {notice}
      <SettingsRow title={enableLabel}><SettingsToggle label={enableLabel} checked={value.enabled} disabled={locked && !value.enabled} onChange={enabled=>update({...value,enabled})}/></SettingsRow>
      {channel === 'desktop' && <SettingsRow title={p('Notification sounds')}><SettingsToggle label={p('Notification sounds')} checked={preferences.soundEnabled} onChange={soundEnabled=>save({...preferences,soundEnabled})}/></SettingsRow>}
      {channel === 'desktop' && typeof Notification !== 'undefined' && Notification.permission !== 'granted' && <SettingsRow title={p('Browser notification permission')}><button className="settings-action" type="button" onClick={async()=>{const permission=await Notification.requestPermission();await save({...preferences,desktopPermission:permission,desktop:{...preferences.desktop,enabled:permission === 'granted'}})}}>{p('Enable browser notifications')}</button></SettingsRow>}
      {channel === 'email' && <SettingsRow title={p('Email delivery')}><SettingsSelect label={p('Email delivery')} value={preferences.emailFormat} options={[{value:'immediate',label:p('Immediately')},{value:'digest',label:p('Digest')}]} onChange={emailFormat=>void save({...preferences,emailFormat:emailFormat as 'immediate'|'digest'})}/></SettingsRow>}
      {channel === 'email' && <SettingsRow title={p('Send urgent notifications immediately')}><SettingsToggle label={p('Send urgent notifications immediately')} checked={preferences.immediateUrgent} onChange={immediateUrgent=>save({...preferences,immediateUrgent})}/></SettingsRow>}
      {channel === 'email' && preferences.emailFormat === 'digest' && <SettingsRow title={p('Delay low priority emails outside of work hours until next work day')}><SettingsToggle label={p('Delay low priority emails outside of work hours until next work day')} checked={preferences.delayLowPriority} onChange={delayLowPriority=>save({...preferences,delayLowPriority})}/></SettingsRow>}
    </SettingsSection>
    <SettingsSection title={p('General notifications')}>{categories.slice(0,11).map(([id,label,description])=><SettingsRow key={id} title={p(label)} description={p(description)}><SettingsToggle label={p(label)} disabled={!value.enabled} checked={value.categories[id] ?? true} onChange={checked=>update({...value,categories:{...value.categories,[id]:checked}})}/></SettingsRow>)}</SettingsSection>
    <SettingsSection title={p('Feature notifications')}>{categories.slice(11).map(([id,label,description])=><SettingsRow key={id} title={p(label)} description={p(description)}><SettingsToggle label={p(label)} disabled={!value.enabled} checked={value.categories[id] ?? true} onChange={checked=>update({...value,categories:{...value.categories,[id]:checked}})}/></SettingsRow>)}</SettingsSection>
  </>
}
