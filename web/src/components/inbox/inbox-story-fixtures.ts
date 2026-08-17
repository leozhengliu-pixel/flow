import type { InboxNotificationRowData } from './notification-row'

export const inboxStoryNotifications: InboxNotificationRowData[] = [
  { id:'inbox-1', actor:'Jiaozong Ben', actorInitials:'JB', kind:'comment', identifier:'CLE-33', title:'Production investigation: scanning flow keeps restoring an old TC number', body:'Jiaozong Ben commented: Fix verified in production and regression coverage added.', timeLabel:'4mo', timestamp:'Mar 28, 14:55', read:false },
  { id:'inbox-2', actor:'Jiaozong Ben', actorInitials:'JB', kind:'comment', identifier:'CLE-26', title:'Backend: supervisor cleaner detail can expose a stale assigned task', body:'Jiaozong Ben commented: Recommended execution order and durable fix direction.', timeLabel:'4mo', timestamp:'Mar 25, 13:22', read:true, favorite:true },
  { id:'inbox-3', actor:'Jiaozong Ben', actorInitials:'JB', kind:'comment', identifier:'CLE-25', title:'Web: supervisor inspection page sporadically shows Task not found', body:'Jiaozong Ben commented: Follow-up implementation ownership updated.', timeLabel:'4mo', timestamp:'Mar 25, 11:15', read:false },
  { id:'inbox-4', actor:'Jiaozong Ben', actorInitials:'JB', kind:'assignment', identifier:'CLE-24', title:'RN Web: optional issues step still forces navigation to Report Issue', body:'Jiaozong Ben assigned the issue to you', timeLabel:'4mo', timestamp:'Mar 24, 11:09', read:true },
  { id:'inbox-5', actor:'Jiaozong Ben', actorInitials:'JB', kind:'assignment', identifier:'CLE-20', title:'Production cleaning task uses the wrong after-room photograph', body:'Jiaozong Ben assigned the issue to you', timeLabel:'5mo', timestamp:'Mar 23, 10:48', read:false },
  { id:'inbox-6', actor:'Jiaozong Ben', actorInitials:'JB', kind:'assignment', identifier:'CLE-18', title:'Bug: page intermittently stops responding to menu actions', body:'Jiaozong Ben assigned the issue to you', timeLabel:'5mo', timestamp:'Mar 19, 14:20', read:true },
]
