import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Search } from 'lucide-react'
import { FLOW_CORE_ICON_NAMES, FLOW_VIEW_ICON_ALIASES, FLOW_VIEW_ICON_NAMES } from './flow-view-icon-data'
import styles from './view-icon-picker.module.css'
import { FLOW_COLOR_PALETTE } from '@/components/ui/color-palette'

export const DEFAULT_VIEW_ICON = 'CustomView'
export const DEFAULT_VIEW_COLOR = '#bec2c8'

const ICON_NAMES: readonly string[] = FLOW_VIEW_ICON_NAMES
const ICON_NAME_SET = new Set<string>(ICON_NAMES)

const FREQUENT_EMOJIS = `👍 👌 🙏 😂 ❤️ 👀 ✅ 🙂 😃 😄 😀 🤔 😅 ⚠️ 😕 ❌ 🙌 🎉 😉 😊 🤷 👋 ❓`.split(' ')
const PEOPLE_EMOJIS = `😀 😃 😄 😁 😆 😅 🤣 😂 🙂 🙃 🫠 😉 😊 😇 🥰 😍 🤩 😘 😗 ☺️ 😚 😙 🥲 😋 😛 😜 🤪 😝 🤑 🤗 🤭 🫢 🫣 🤫 🤔 🫡 🤐 🤨 😐 😑 😶 😶‍🌫️ 😏 😒 🙄 😬 😮‍💨 🤥 😌 😔 😪 🤤 😴 😷 🤒 🤕 🤢 🤮 🤧 🥵 🥶 🥴 😵 😵‍💫 🤯 🤠 🥳 🥸 😎 🤓 🧐 😕 🫤 😟 🙁 ☹️ 😮 😯 😲 😳 🥺 🥹 😦 😧 😨 😰 😥 😢 😭 😱 😖 😣 😞 😓 😩 😫 🥱 😤 😡 😠 🤬 😈 👿 💀 ☠️ 💩 🤡 👻 👽 👾 🤖 👋 🤚 🖐️ ✋ 🖖 🫱 🫲 🫳 🫴 👌 🤌 🤏 ✌️ 🤞 🫰 🤟 🤘 🤙 👈 👉 👆 🖕 👇 ☝️ 🫵 👍 👎 ✊ 👊 🤛 🤜 👏 🙌 🫶 👐 🤲 🤝 🙏 ✍️ 💅`.split(' ')
const EMOJI_NAMES: Record<string, string> = { '👍': '+1', '👌': 'ok_hand', '🙏': 'pray', '😂': 'joy', '❤️': 'heart', '👀': 'eyes', '✅': 'white_check_mark', '🙂': 'slightly_smiling_face', '⚠️': 'warning', '❌': 'x', '🙌': 'raised_hands', '🎉': 'tada', '🤷': 'shrug', '👋': 'wave', '❓': 'question' }

export type ViewVisual = { icon: string; color: string }

export function ViewGlyph({ className, color = DEFAULT_VIEW_COLOR, icon = DEFAULT_VIEW_ICON }: { className?: string; color?: string; icon?: string }) {
  const assetIcon = FLOW_VIEW_ICON_ALIASES[icon] ?? icon
  if (!ICON_NAME_SET.has(assetIcon) && assetIcon !== 'Team') return isEmoji(icon)
    ? <span aria-hidden="true" className={`${styles.glyph} ${styles.emojiGlyph} ${className ?? ''}`} style={{ color }}>{icon}</span>
    : <svg aria-hidden="true" className={`${styles.glyph} ${className ?? ''}`} fill="currentColor" style={{ color }} viewBox="0 0 16 16"><use href="/flow-core-icons.svg#CustomView"/></svg>
  return <svg aria-hidden="true" className={`${styles.glyph} ${className ?? ''}`} fill="currentColor" style={{ color }} viewBox="0 0 16 16"><use href={`${FLOW_CORE_ICON_NAMES.has(assetIcon) ? '/flow-core-icons.svg' : '/flow-view-icons.svg'}#${assetIcon}`}/></svg>
}

export function ViewIconPicker({ align = 'start', color = DEFAULT_VIEW_COLOR, icon = DEFAULT_VIEW_ICON, onChange, prependTeam = false, triggerClassName }: { align?: 'start' | 'center' | 'end'; color?: string; icon?: string; onChange: (visual: ViewVisual) => void; prependTeam?: boolean; triggerClassName?: string }) {
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<'icons' | 'emojis'>('icons')
  const [query, setQuery] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const customColorRef = useRef<HTMLInputElement>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  const normalizedQuery = query.trim().toLowerCase()
  const filteredIcons = useMemo(() => (prependTeam ? ['Team', ...ICON_NAMES.filter(name => name !== 'Team')] : ICON_NAMES).filter(name => name.toLowerCase().includes(normalizedQuery)), [normalizedQuery, prependTeam])
  const emojiSections = useMemo(() => [
    { label: 'Frequently used', values: FREQUENT_EMOJIS.filter(value => matchesEmoji(value, normalizedQuery)) },
    { label: 'Smileys & People', values: PEOPLE_EMOJIS.filter(value => matchesEmoji(value, normalizedQuery)) },
  ].filter(section => section.values.length), [normalizedQuery])

  useEffect(() => { if (!open) { setQuery(''); setTab('icons') } }, [open])

  const chooseColor = (nextColor: string) => onChange({ icon, color: nextColor.toLowerCase() })
  const chooseIcon = (nextIcon: string) => { onChange({ icon: nextIcon, color }); setOpen(false) }
  const focusGridItem = (index: number) => {
    const items = gridRef.current?.querySelectorAll<HTMLButtonElement>('button:not([disabled])')
    if (!items?.length) return
    items[Math.max(0, Math.min(items.length - 1, index))]?.focus()
  }
  const moveGridFocus = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const columns = 14
    const next = event.key === 'ArrowRight' ? index + 1 : event.key === 'ArrowLeft' ? index - 1 : event.key === 'ArrowDown' ? index + columns : event.key === 'ArrowUp' ? index - columns : index
    if (next === index) return
    event.preventDefault(); focusGridItem(next)
  }

  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><button aria-label="Choose icon" className={`${styles.trigger} ${triggerClassName ?? ''}`} data-state={open ? 'open' : 'closed'} style={{ '--view-color': color } as CSSProperties} type="button"><ViewGlyph color={color} icon={icon}/></button></Popover.Trigger>
    <Popover.Portal><Popover.Content align={align} className={styles.content} collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => searchRef.current?.focus()) }} side="bottom" sideOffset={4}>
      <div aria-label="Icon type" className={styles.tabs} role="tablist">
        <button aria-controls="view-icons-panel" aria-selected={tab === 'icons'} className={styles.tab} onClick={() => { setTab('icons'); setQuery(''); requestAnimationFrame(() => searchRef.current?.focus()) }} role="tab" type="button">Icons</button>
        <button aria-controls="view-emojis-panel" aria-selected={tab === 'emojis'} className={styles.tab} onClick={() => { setTab('emojis'); setQuery(''); requestAnimationFrame(() => searchRef.current?.focus()) }} role="tab" type="button">Emojis</button>
      </div>
      {tab === 'icons' && <div aria-label="Icons" className={styles.panel} id="view-icons-panel" role="tabpanel">
        <div className={styles.colors}>{FLOW_COLOR_PALETTE.map(option => <button aria-label={option.name} className={styles.colorButton} data-selected={color.toLowerCase() === option.value} key={option.value} onClick={() => chooseColor(option.value)} type="button"><span style={{ background: option.value }}>{color.toLowerCase() === option.value && <CheckMark/>}</span></button>)}<button aria-label="Set custom color" className={`${styles.colorButton} ${styles.customColor}`} data-selected={!FLOW_COLOR_PALETTE.some(option => option.value === color.toLowerCase())} onClick={() => customColorRef.current?.click()} type="button"><span/>{!FLOW_COLOR_PALETTE.some(option => option.value === color.toLowerCase()) && <i/>}</button><input aria-hidden="true" className={styles.colorInput} onChange={event => chooseColor(event.target.value)} ref={customColorRef} tabIndex={-1} type="color" value={validColor(color) ? color : DEFAULT_VIEW_COLOR}/></div>
        <SearchBox onArrowDown={() => focusGridItem(0)} placeholder="Search icons…" query={query} searchRef={searchRef} setQuery={setQuery}/>
        <div className={styles.iconGrid} ref={gridRef}>{filteredIcons.map((name, index) => <button aria-label={name} className={styles.iconButton} data-selected={icon === name} key={name} onClick={() => chooseIcon(name)} onKeyDown={event => moveGridFocus(event, index)} title={name} type="button"><ViewGlyph color={color} icon={name}/></button>)}</div>
      </div>}
      {tab === 'emojis' && <div aria-label="Emojis" className={`${styles.panel} ${styles.emojiPanel}`} id="view-emojis-panel" role="tabpanel">
        <SearchBox onArrowDown={() => focusGridItem(0)} placeholder="Search emoji…" query={query} searchRef={searchRef} setQuery={setQuery}/>
        <div className={styles.emojiScroller} ref={gridRef}>{emojiSections.map(section => <section className={styles.emojiSection} key={section.label}><h3>{section.label}</h3><div className={styles.emojiGrid}>{section.values.map((value, index) => <button aria-label={EMOJI_NAMES[value] ?? `emoji_${value.codePointAt(0)?.toString(16)}`} data-selected={icon === value} key={`${section.label}:${value}:${index}`} onClick={() => chooseIcon(value)} onKeyDown={event => moveGridFocus(event, index)} type="button"><span>{value}</span></button>)}</div></section>)}</div>
      </div>}
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function SearchBox({ onArrowDown, placeholder, query, searchRef, setQuery }: { onArrowDown: () => void; placeholder: string; query: string; searchRef: React.RefObject<HTMLInputElement | null>; setQuery: (query: string) => void }) {
  return <label className={styles.search}><input aria-label={placeholder} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'ArrowDown') { event.preventDefault(); onArrowDown() } }} placeholder={placeholder} ref={searchRef} value={query}/><Search aria-hidden="true" size={13}/></label>
}

function CheckMark() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 10 8"><path d="M3.47 5.708 1.884 4.123a.576.576 0 0 0-.815.814l1.996 1.994a.576.576 0 0 0 .814 0L8.931 1.883a.576.576 0 0 0-.815-.814L3.47 5.708Z"/></svg> }
function matchesEmoji(value: string, query: string) { return !query || value.includes(query) || (EMOJI_NAMES[value] ?? '').includes(query.replaceAll(' ', '_')) }
function validColor(value: string) { return /^#[0-9a-f]{6}$/i.test(value) }
function isEmoji(value: string) { return /\p{Extended_Pictographic}/u.test(value) }
