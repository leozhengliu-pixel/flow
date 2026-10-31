import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ToolbarGlyph } from '@/components/issue/editor/selection-toolbar-glyphs'
import { Avatar } from '@/components/issue/issue-row'
import { AddReactionIcon } from '@/components/issue/issue-icons'
import { FlowOptionsIcon } from '@/components/issue/flow-header-icons'
import { HealthGlyph } from '@/components/project-detail/health-glyph'
import { RichComment } from '@/components/activity/rich-comment'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { toggleInitiativeUpdateReaction } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { formatRelativeTime } from '@/lib/relative-time'
import type { Initiative, InitiativeUpdate, User } from '@/types/flow'

export function InitiativeLatestUpdate({ initiative, update, viewer, onOpen, onReload }: {
  initiative: Initiative
  update: InitiativeUpdate
  viewer: User
  onOpen: () => void
  onReload: () => Promise<void>
}) {
  const { t, locale } = useI18n()
  const now = useCurrentTimeMs()
  const timeLabel = now - new Date(update.createdAt).getTime() < 3_600_000
    ? activityTimeLabel(update.createdAt, now, locale)
    : formatRelativeTime(update.createdAt, locale, now)
  const react = async (emoji: string) => {
    await toggleInitiativeUpdateReaction(initiative.id, update.id, emoji)
    await onReload()
  }
  return <article className="li-latest-update" data-testid="initiative-latest-update">
    <header><h3>{t('Latest update')}</h3><button aria-label={t('Write initiative update')} onClick={onOpen} title={t('Write initiative update')} type="button"><svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M7.25 1C7.66414 1 7.99988 1.33589 8 1.75C8 2.16421 7.66421 2.5 7.25 2.5H4.75C3.50745 2.5 2.50012 3.50744 2.5 4.75V11.25C2.5 12.4926 3.50736 13.5 4.75 13.5H11.25C12.4926 13.5 13.5 12.4926 13.5 11.25V8.75C13.5001 8.33589 13.8359 8 14.25 8C14.6641 8 14.9999 8.33589 15 8.75V11.25C15 13.3211 13.3211 15 11.25 15H4.75C2.67893 15 1 13.3211 1 11.25V4.75C1.00012 2.67905 2.67899 1 4.75 1H7.25Z"/><path d="M13.4326 1.26953C13.7913 0.910937 14.3728 0.910883 14.7314 1.26953C15.0897 1.6282 15.0899 2.20981 14.7314 2.56836L9.2373 8.06152C8.68101 8.6177 7.94043 8.95161 7.15527 9C7.06754 9.0052 6.99468 8.93248 7 8.84473C7.04847 8.05961 7.38232 7.31897 7.93848 6.7627L13.4326 1.26953Z"/></svg></button></header>
    <div className="li-latest-update__content">
      <div className="li-latest-update__meta">
        <div className={`li-latest-update__health is-${update.health}`}><HealthGlyph health={update.health}/><span>{t(({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No updates' } as const)[update.health])}</span></div>
        <div className="li-latest-update__author"><Avatar name={update.user.displayName || update.user.name}/><span data-i18n-ignore>{update.user.displayName || update.user.name}</span></div>
        <time dateTime={update.createdAt} title={new Date(update.createdAt).toLocaleString(locale)}>{timeLabel}</time>
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Update actions')} title={t('Update actions')} type="button"><FlowOptionsIcon/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu" data-flow-motion="floating" align="end"><DropdownMenu.Item onSelect={onOpen}>{t('View initiative updates')}</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(`${window.location.href}#${update.id}`)}>{t('Copy link')}</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
      </div>
      <div className="li-latest-update__body" data-i18n-ignore><RichComment body={update.body} data={update.bodyData}/></div>
      <footer><button aria-label={t('Comments')} className="li-latest-update__comments ui-pill" onClick={onOpen} type="button"><ToolbarGlyph name="comment"/>{update.comments.length || null}</button><EmojiPicker onSelect={react}><button aria-label={t('Add reaction')} title={t('Add reaction')} type="button"><AddReactionIcon size={14}/></button></EmojiPicker><ReactionPills reactions={update.reactions} viewerId={viewer.id} onToggle={react}/></footer>
    </div>
  </article>
}
