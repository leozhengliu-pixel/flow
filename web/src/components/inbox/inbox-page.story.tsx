import { useState } from 'react'

import { InboxDetailActivity } from './inbox-detail-preview'
import { InboxPage, type InboxPageAdapter } from './inbox-page'
import type { InboxDisplayOptions } from './inbox-page-shell'
import type { InboxNotificationRowData } from './notification-row'
import { inboxStoryNotifications } from './inbox-story-fixtures'

const defaultDisplay: InboxDisplayOptions = { ordering:'newest', showSnoozed:false, showRead:true, showUnreadFirst:false }
const wait = () => new Promise<void>(resolve => window.setTimeout(resolve, 180))
const storyAdapter: InboxPageAdapter = { setRead:wait, delete:wait, snooze:wait, setFavorite:wait, deleteAll:wait, deleteAllRead:wait, deleteAllReadCompleted:wait }

export function InboxPageStory({ initialSelectedId = null }: { initialSelectedId?: string | null }) {
  const [notifications,setNotifications]=useState(inboxStoryNotifications)
  const [selectedId,setSelectedId]=useState<string|null>(initialSelectedId)
  const [displayOptions,setDisplayOptions]=useState(defaultDisplay)
  return <div className="flow-inbox-story"><InboxPage
    notifications={notifications}
    selectedId={selectedId}
    onNotificationsChange={setNotifications}
    onSelectedIdChange={setSelectedId}
    adapter={storyAdapter}
    displayOptions={displayOptions}
    onDisplayOptionsChange={setDisplayOptions}
    onAddFilter={()=>undefined}
    onRetryLoad={()=>undefined}
    onOpenIssue={()=>undefined}
    onCopyLink={()=>undefined}
    onCopyIdentifier={()=>undefined}
    subscribed={()=>true}
    onSubscribeChange={()=>undefined}
    renderDetail={notification=>({content:<StoryDetail notification={notification}/>})}
  /></div>
}

function StoryDetail({ notification }: { notification: InboxNotificationRowData }) {
  return <><section className="flow-inbox-story__issue"><h1>{notification.title}</h1><p>This deterministic fixture reserves the same content width as the shared Issue detail engine.</p></section><InboxDetailActivity actor={notification.actor} time="4mo ago"><h2>Recommended execution order</h2><p>This is the more complete and durable fix direction.</p><h3>Why this matters</h3><p>The issue is a modeling inconsistency between the aggregate view and its representative task.</p></InboxDetailActivity></>
}
