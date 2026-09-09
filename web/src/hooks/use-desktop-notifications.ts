import { useEffect } from 'react'

import { acknowledgeDesktopNotifications } from '@/lib/api'
import { issuePath } from '@/lib/app-routes'
import type { BootstrapData } from '@/types/flow'
import { playNotificationSound } from '@/lib/notification-sound'

export function useDesktopNotifications(data: BootstrapData | null) {
  useEffect(() => {
    if (!data || !('Notification' in window) || Notification.permission !== 'granted') return
    const preferences = data.notificationPreferences?.[data.viewer.id]
    if (!preferences?.desktop.enabled) return
    const pending = (data.notificationDeliveries ?? []).filter(item => item.channel === 'desktop' && item.status === 'pending' && item.recipientId === data.viewer.id)
    const delivered: string[] = []
    for (const delivery of pending) {
      const marker = `flow:desktop-delivery:${delivery.id}`
      if (localStorage.getItem(marker)) continue
      const source = data.notifications.find(item => item.id === delivery.notificationId)
      const issue = source ? data.issues.find(item => item.id === source.issueId) : undefined
      if (!source) continue
      const body = source.type === 'assignment' ? `${source.actor.displayName} assigned this issue to you` : source.type === 'mention' ? `${source.actor.displayName} mentioned you` : source.type === 'comment' ? `${source.actor.displayName} commented on this issue` : `${source.actor.displayName} updated this issue`
      const project = data.projects.find(item=>item.id===source.projectId)
      const notification = new Notification(issue ? `${issue.identifier} ${issue.title}` : project?.name ?? data.workspace.name, { body, tag: source.groupKey || source.id })
      localStorage.setItem(marker, new Date().toISOString())
      notification.onclick = () => { window.focus(); window.location.assign(issue ? issuePath(data.workspace.urlKey, issue) : `/${encodeURIComponent(data.workspace.urlKey)}/inbox?notification=${encodeURIComponent(source.id)}`); notification.close() }
      delivered.push(source.id)
    }
    if (delivered.length) {
      if (preferences.soundEnabled) void playNotificationSound()
      void acknowledgeDesktopNotifications(delivered)
    }
  }, [data])
}
