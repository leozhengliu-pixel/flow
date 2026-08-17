import { useMemo, useState } from 'react'
import { Check, Mail, Monitor, RotateCcw } from 'lucide-react'
import { toast } from 'sonner'

import { retryNotificationDelivery, updateNotificationPreferences } from '@/lib/api'
import type { BootstrapData, NotificationCategory, NotificationPreferences } from '@/types/flow'

const CATEGORIES: { id: NotificationCategory; label: string; description: string }[] = [
  { id: 'assignments', label: 'Assignments', description: 'Issues and projects assigned to you' },
  { id: 'statusChanges', label: 'Status changes', description: 'Status changes on work you follow' },
  { id: 'comments', label: 'Comments and replies', description: 'New comments and replies on subscribed work' },
  { id: 'mentions', label: 'Mentions', description: 'When someone mentions you' },
  { id: 'reactions', label: 'Reactions', description: 'Reactions to your comments and updates' },
  { id: 'subscriptions', label: 'Subscriptions', description: 'Changes to issues and projects you subscribe to' },
  { id: 'documents', label: 'Document changes', description: 'Changes to documents you follow' },
  { id: 'updates', label: 'Updates', description: 'Project and initiative updates' },
  { id: 'reminders', label: 'Reminders and deadlines', description: 'Due dates, reminders, and SLA alerts' },
  { id: 'loops', label: 'Loops', description: 'Loop reminders and responses' },
  { id: 'integrations', label: 'Apps and integrations', description: 'Activity from connected applications' },
  { id: 'billing', label: 'Billing', description: 'Plan, invoice, and usage notifications' },
  { id: 'customerRequests', label: 'Customer requests', description: 'Changes to linked customer requests' },
  { id: 'triage', label: 'Triage', description: 'New and updated issues in Triage' },
]

export function NotificationSettings({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const initial = data.notificationPreferences?.[data.viewer.id] ?? defaultPreferences(data.viewer.id)
  const [preferences, setPreferences] = useState(initial)
  const [channel, setChannel] = useState<'email'|'desktop'>('email')
  const [busy, setBusy] = useState(false)
  const deliveries = useMemo(() => (data.notificationDeliveries ?? []).filter(item => item.recipientId === data.viewer.id && item.channel === channel && (item.status === 'failed' || item.status === 'pending-disabled')).slice(0, 4), [channel, data.notificationDeliveries, data.viewer.id])

  const save = async (next: NotificationPreferences) => {
    setPreferences(next); setBusy(true)
    try { setPreferences(await updateNotificationPreferences(next)); await onReload() }
    catch (error) { setPreferences(preferences); toast.error(error instanceof Error ? error.message : 'Could not save notification settings') }
    finally { setBusy(false) }
  }
  const setEnabled = async (enabled: boolean) => {
    let permission = preferences.desktopPermission
    if (channel === 'desktop' && enabled) {
      if (!('Notification' in window)) { toast.error('Desktop notifications are not supported by this browser'); return }
      permission = await Notification.requestPermission()
      if (permission !== 'granted') { toast.error('Allow notifications in your browser settings to enable this channel'); return }
    }
    await save({ ...preferences, [channel]: { ...preferences[channel], enabled }, desktopPermission: permission })
  }
  const setCategory = (id: NotificationCategory, enabled: boolean) => void save({ ...preferences, [channel]: { ...preferences[channel], categories: { ...preferences[channel].categories, [id]: enabled } } })

  return <>
    <header className="settings-page-header"><div><h1>Notifications</h1><p>Choose which notifications you receive and how they are delivered.</p></div></header>
    <div className="notification-channel-tabs" role="tablist">
      <button role="tab" aria-selected={channel === 'email'} onClick={() => setChannel('email')}><Mail size={15}/>Email</button>
      <button role="tab" aria-selected={channel === 'desktop'} onClick={() => setChannel('desktop')}><Monitor size={15}/>Desktop</button>
    </div>
    <section className="settings-section"><div className="settings-card">
      <div className="settings-row"><div><strong>Enable {channel} notifications</strong><span>{channel === 'email' ? 'Receive notifications at '+data.viewer.email : 'Show system notifications while Flow is open'}</span></div><div className="settings-control"><Toggle checked={preferences[channel].enabled} label={`Enable ${channel} notifications`} onChange={value => void setEnabled(value)}/></div></div>
      {channel === 'email' && <>
        <div className="settings-row"><div><strong>Format</strong><span>Send notifications immediately or as a digest</span></div><div className="settings-control"><div className="settings-segmented"><button className={preferences.emailFormat === 'immediate' ? 'active' : ''} onClick={() => void save({ ...preferences, emailFormat: 'immediate' })}>Immediate</button><button className={preferences.emailFormat === 'digest' ? 'active' : ''} onClick={() => void save({ ...preferences, emailFormat: 'digest' })}>Digest</button></div></div></div>
        <PreferenceToggle title="Delay low-priority email outside work hours" checked={preferences.delayLowPriority} onChange={value => void save({ ...preferences, delayLowPriority: value })}/>
        <PreferenceToggle title="Send urgent and SLA notifications immediately" checked={preferences.immediateUrgent} onChange={value => void save({ ...preferences, immediateUrgent: value })}/>
      </>}
      {channel === 'desktop' && <PreferenceToggle title="Notification sounds" checked={preferences.soundEnabled} onChange={value => void save({ ...preferences, soundEnabled: value })}/>} 
    </div></section>
    <section className="settings-section"><h3>Notify me about</h3><div className={`settings-card notification-category-list${!preferences[channel].enabled ? ' disabled' : ''}`}>
      {CATEGORIES.map(item => <button key={item.id} disabled={!preferences[channel].enabled || busy} onClick={() => setCategory(item.id, !preferences[channel].categories[item.id])}><span className="notification-category-check" data-checked={preferences[channel].categories[item.id]}>{preferences[channel].categories[item.id] && <Check size={12}/>}</span><span><strong>{item.label}</strong><small>{item.description}</small></span></button>)}
    </div></section>
    {deliveries.length > 0 && <section className="settings-section"><h3>Delivery issues</h3><div className="settings-card">{deliveries.map(item => <div className="settings-row" key={item.id}><div><strong>{item.status === 'pending-disabled' ? 'Delivery is not configured' : 'Delivery failed'}</strong><span>{item.error || (channel === 'email' ? 'Configure SMTP to send email notifications.' : 'Grant browser notification permission.')}</span></div><button className="settings-action" onClick={() => void retryNotificationDelivery(item.id).then(onReload)}><RotateCcw size={13}/>Retry</button></div>)}</div></section>}
  </>
}

function PreferenceToggle({ title, checked, onChange }: { title: string; checked: boolean; onChange: (value: boolean) => void }) { return <div className="settings-row"><div><strong>{title}</strong></div><div className="settings-control"><Toggle checked={checked} label={title} onChange={onChange}/></div></div> }
function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (value: boolean) => void; label: string }) { return <button type="button" role="switch" aria-label={label} aria-checked={checked} className="settings-toggle" onClick={() => onChange(!checked)}><span/></button> }
function defaultPreferences(userId: string): NotificationPreferences { const categories = Object.fromEntries(CATEGORIES.map(item => [item.id, true])) as NotificationPreferences['email']['categories']; return { userId, inbox: { enabled: true, categories: { ...categories } }, email: { enabled: true, categories: { ...categories } }, desktop: { enabled: true, categories: { ...categories } }, emailFormat: 'digest', delayLowPriority: true, immediateUrgent: true, soundEnabled: true, updatedAt: new Date().toISOString() } }
