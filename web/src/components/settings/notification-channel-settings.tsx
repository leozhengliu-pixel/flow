import { ArrowLeft } from 'lucide-react'
import type { NotificationCategory, NotificationPreferences } from '@/types/flow'
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
]

export function NotificationChannelSettings({channel,preferences,save,onBack,p}:{channel:'desktop'|'email';preferences:NotificationPreferences;save:(value:NotificationPreferences)=>Promise<void>;onBack:()=>void;p:(text:string)=>string}) {
  const value=preferences[channel]
  return <>
    <button className="settings-notification-back" type="button" onClick={onBack}><ArrowLeft size={14}/>{p('Notifications')}</button>
    <SettingsPageTitle description={p(channel === 'desktop' ? 'Applies across your browsers with notifications enabled' : 'Notifications delivered to your email address')}>{p(channel === 'desktop' ? 'Desktop' : 'Email')}</SettingsPageTitle>
    <SettingsSection title={p('Settings')}>
      <SettingsRow title={p(`Enable ${channel} notifications`)}><SettingsToggle label={p(`Enable ${channel} notifications`)} checked={value.enabled} onChange={enabled=>save({...preferences,[channel]:{...value,enabled}})}/></SettingsRow>
      {channel === 'desktop' && <SettingsRow title={p('Notification sounds')}><SettingsToggle label={p('Notification sounds')} checked={preferences.soundEnabled} onChange={soundEnabled=>save({...preferences,soundEnabled})}/></SettingsRow>}
      {channel === 'desktop' && typeof Notification !== 'undefined' && Notification.permission !== 'granted' && <SettingsRow title={p('Browser notification permission')}><button className="settings-action" type="button" onClick={async()=>{const permission=await Notification.requestPermission();await save({...preferences,desktopPermission:permission,desktop:{...value,enabled:permission === 'granted'}})}}>{p('Enable browser notifications')}</button></SettingsRow>}
      {channel === 'email' && <SettingsRow title={p('Email delivery')}><SettingsSelect label={p('Email delivery')} value={preferences.emailFormat} options={[{value:'immediate',label:p('Immediately')},{value:'digest',label:p('Digest')}]} onChange={emailFormat=>void save({...preferences,emailFormat:emailFormat as 'immediate'|'digest'})}/></SettingsRow>}
      {channel === 'email' && <SettingsRow title={p('Send urgent notifications immediately')}><SettingsToggle label={p('Send urgent notifications immediately')} checked={preferences.immediateUrgent} onChange={immediateUrgent=>save({...preferences,immediateUrgent})}/></SettingsRow>}
      {channel === 'email' && preferences.emailFormat === 'digest' && <SettingsRow title={p('Delay low priority emails outside of work hours until next work day')}><SettingsToggle label={p('Delay low priority emails outside of work hours until next work day')} checked={preferences.delayLowPriority} onChange={delayLowPriority=>save({...preferences,delayLowPriority})}/></SettingsRow>}
    </SettingsSection>
    <SettingsSection title={p('General notifications')}>{categories.slice(0,11).map(([id,label,description])=><SettingsRow key={id} title={p(label)} description={p(description)}><SettingsToggle label={p(label)} disabled={!value.enabled} checked={value.categories[id] ?? true} onChange={checked=>save({...preferences,[channel]:{...value,categories:{...value.categories,[id]:checked}}})}/></SettingsRow>)}</SettingsSection>
    <SettingsSection title={p('Feature notifications')}>{categories.slice(11).map(([id,label,description])=><SettingsRow key={id} title={p(label)} description={p(description)}><SettingsToggle label={p(label)} disabled={!value.enabled} checked={value.categories[id] ?? true} onChange={checked=>save({...preferences,[channel]:{...value,categories:{...value.categories,[id]:checked}}})}/></SettingsRow>)}</SettingsSection>
  </>
}
