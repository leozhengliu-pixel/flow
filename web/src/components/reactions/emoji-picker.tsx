import * as Popover from '@radix-ui/react-popover'
import { Search, SmilePlus } from 'lucide-react'
import { useMemo, useState, type ReactNode, type Ref } from 'react'

const frequentlyUsed = ['👍', '👌', '🙏', '😂', '❤️', '👀', '✅', '🙂', '😃', '😄', '😀', '🤔', '😅', '⚠️', '😕', '❌', '🙌', '🎉']
const groups = [
  { name: 'Smileys & People', emojis: ['😀','😃','😄','😁','😆','😅','🤣','😂','🙂','🙃','🫠','😉','😊','😇','🥰','😍','🤩','😘','😗','☺️','😚','😙','🥲','😋','😛','😜','🤪','😝','🤑','🤗','🤭','🫢','🫣','🤫','🤔','🫡','🤐','🤨','😐','😑','😶','🫥','😶‍🌫️','😏','😒','🙄','😬','😮‍💨','🤥','😌','😔','😪','🤤','😴','😷','🤒','🤕','🤢','🤮','🤧','🥵','🥶','🥴','😵','😵‍💫','🤯','🥳','😎','🤓','🧐','😕','🙁','☹️','😮','😯','😲','😳'] },
  { name: 'Gestures', emojis: ['👍','👎','👌','🤌','🤏','✌️','🤞','🫰','🤟','🤘','🤙','👈','👉','👆','👇','☝️','✋','🤚','🖐️','🖖','👋','🤝','👏','🙌','🫶','🙏'] },
  { name: 'Objects & Symbols', emojis: ['❤️','🧡','💛','💚','💙','💜','🖤','🤍','💔','❣️','💕','💯','💥','💫','💦','🔥','✨','🎉','🎊','✅','❌','⚠️','🚀','👀','💡','📌','🔒'] },
]

const emojiNames: Record<string, string> = { '👍':'thumbs up +1 like', '👌':'ok hand', '🙏':'pray thanks', '😂':'joy laugh', '❤️':'heart love', '👀':'eyes look', '✅':'white check mark done', '🤔':'thinking face', '⚠️':'warning', '❌':'x close', '🙌':'raised hands', '🎉':'tada party', '🚀':'rocket', '🔥':'fire', '💡':'idea' }

export function EmojiPicker({ children, contentRef, label = 'Add reaction', align = 'start', onSelect }: { children?: ReactNode; contentRef?: Ref<HTMLDivElement>; label?: string; align?: 'start'|'center'|'end'; onSelect: (emoji: string) => void | Promise<void> }) {
  const [open, setOpen] = useState(false), [query, setQuery] = useState('')
  const normalized = query.trim().toLowerCase()
  const visibleGroups = useMemo(() => groups.map(group => ({ ...group, emojis: group.emojis.filter(emoji => !normalized || emoji.includes(normalized) || (emojiNames[emoji] ?? '').includes(normalized)) })).filter(group => group.emojis.length), [normalized])
  const choose = async (emoji: string) => {
    try { await onSelect(emoji) } finally { setOpen(false); setQuery('') }
  }
  return <Popover.Root open={open} onOpenChange={value => { setOpen(value); if (!value) setQuery('') }}>
    <Popover.Trigger asChild>{children ?? <button type="button" aria-label={label}><SmilePlus size={14}/>{label}</button>}</Popover.Trigger>
    <Popover.Portal><Popover.Content className="emoji-picker" ref={contentRef} side="bottom" align={align} sideOffset={4} collisionPadding={10} onOpenAutoFocus={event => event.preventDefault()}>
      <div className="emoji-search"><Search size={13}/><input autoFocus value={query} onChange={event => setQuery(event.target.value)} placeholder="Search emoji…" aria-label="Search emoji…"/></div>
      <div className="emoji-scroll">
        {!normalized && <EmojiGroup name="Frequently used" emojis={frequentlyUsed} onSelect={emoji => void choose(emoji)}/>}
        {visibleGroups.map(group => <EmojiGroup key={group.name} {...group} onSelect={emoji => void choose(emoji)}/>)}
        {!visibleGroups.length && <div className="emoji-empty">No emoji found</div>}
      </div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function EmojiGroup({ name, emojis, onSelect }: { name: string; emojis: string[]; onSelect: (emoji: string) => void }) {
  return <section className="emoji-group"><h4>{name}</h4><div>{emojis.map((emoji, index) => <Popover.Close asChild key={`${emoji}-${index}`}><button type="button" aria-label={emojiNames[emoji] ?? `emoji ${index + 1}`} title={emojiNames[emoji]} onClick={() => onSelect(emoji)}>{emoji}</button></Popover.Close>)}</div></section>
}

export function ReactionPills({ reactions = {}, viewerId, onToggle }: { reactions?: Record<string,string[]>; viewerId: string; onToggle: (emoji:string)=>void|Promise<void> }) {
  if (!Object.keys(reactions).length) return null
  return <div className="reaction-pills">{Object.entries(reactions).map(([emoji, users]) => <button type="button" key={emoji} aria-pressed={users.includes(viewerId)} title={`${users.length} reaction${users.length === 1 ? '' : 's'}`} onClick={() => void onToggle(emoji)}><span>{emoji}</span>{users.length}</button>)}</div>
}
