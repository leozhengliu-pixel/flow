import { useEffect } from 'react'

import { acknowledgeDesktopNotifications } from '@/lib/api'
import { issuePath } from '@/lib/app-routes'
import type { BootstrapData } from '@/types/flow'

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
      localStorage.setItem(marker, new Date().toISOString())
      const source = data.notifications.find(item => item.id === delivery.notificationId)
      const issue = source ? data.issues.find(item => item.id === source.issueId) : undefined
      if (!source || !issue) continue
      const body = source.type === 'assignment' ? `${source.actor.displayName} assigned this issue to you` : source.type === 'mention' ? `${source.actor.displayName} mentioned you` : source.type === 'comment' ? `${source.actor.displayName} commented on this issue` : `${source.actor.displayName} updated this issue`
      const notification = new Notification(`${issue.identifier} ${issue.title}`, { body, tag: source.groupKey || source.id })
      notification.onclick = () => { window.focus(); window.location.assign(issuePath(data.workspace.urlKey, issue)); notification.close() }
      delivered.push(source.id)
    }
    if (delivered.length) void acknowledgeDesktopNotifications(delivered)
  }, [data])
}
