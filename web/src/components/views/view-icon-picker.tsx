import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Search } from 'lucide-react'
import { FLOW_VIEW_ICON_ALIASES, FLOW_VIEW_ICON_NAMES } from './flow-view-icon-data'
import styles from './view-icon-picker.module.css'
import { ensureViewIconSprite } from './view-icon-sprite'
import { useI18n } from '@/i18n/i18n'
import { ColorSwatchPicker } from '@/components/ui/color-swatch-picker'

export const DEFAULT_VIEW_ICON = 'CustomView'
export const DEFAULT_VIEW_COLOR = '#bec2c8'

const ICON_NAMES: readonly string[] = FLOW_VIEW_ICON_NAMES
const ICON_NAME_SET = new Set<string>(ICON_NAMES)

const FREQUENT_EMOJIS = `👍 👌 🙏 😂 ❤️ 👀 ✅ 🙂 😃 😄 😀 🤔 😅 ⚠️ 😕 ❌ 🙌 🎉 😉 😊 🤷 👋 ❓`.split(' ')
const PEOPLE_EMOJIS = `😀 😃 😄 😁 😆 😅 🤣 😂 🙂 🙃 🫠 😉 😊 😇 🥰 😍 🤩 😘 😗 ☺️ 😚 😙 🥲 😋 😛 😜 🤪 😝 🤑 🤗 🤭 🫢 🫣 🤫 🤔 🫡 🤐 🤨 😐 😑 😶 😶‍🌫️ 😏 😒 🙄 😬 😮‍💨 🤥 😌 😔 😪 🤤 😴 😷 🤒 🤕 🤢 🤮 🤧 🥵 🥶 🥴 😵 😵‍💫 🤯 🤠 🥳 🥸 😎 🤓 🧐 😕 🫤 😟 🙁 ☹️ 😮 😯 😲 😳 🥺 🥹 😦 😧 😨 😰 😥 😢 😭 😱 😖 😣 😞 😓 😩 😫 🥱 😤 😡 😠 🤬 😈 👿 💀 ☠️ 💩 🤡 👻 👽 👾 🤖 👋 🤚 🖐️ ✋ 🖖 🫱 🫲 🫳 🫴 👌 🤌 🤏 ✌️ 🤞 🫰 🤟 🤘 🤙 👈 👉 👆 🖕 👇 ☝️ 🫵 👍 👎 ✊ 👊 🤛 🤜 👏 🙌 🫶 👐 🤲 🤝 🙏 ✍️ 💅`.split(' ')
const EMOJI_NAMES: Record<string, string> = { '👍': '+1', '👌': 'ok_hand', '🙏': 'pray', '😂': 'joy', '❤️': 'heart', '👀': 'eyes', '✅': 'white_check_mark', '🙂': 'slightly_smiling_face', '⚠️': 'warning', '❌': 'x', '🙌': 'raised_hands', '🎉': 'tada', '🤷': 'shrug', '👋': 'wave', '❓': 'question' }

export type ViewVisual = { icon: string; color: string }

export function ViewGlyph({ className, color = DEFAULT_VIEW_COLOR, icon = DEFAULT_VIEW_ICON, style }: { className?: string; color?: string; icon?: string; style?: CSSProperties }) {
  const assetIcon = FLOW_VIEW_ICON_ALIASES[icon] ?? icon
  useEffect(() => { void ensureViewIconSprite() }, [])
  if (!ICON_NAME_SET.has(assetIcon) && assetIcon !== 'Team') return isEmoji(icon)
    ? <span aria-hidden="true" className={`${styles.glyph} ${styles.emojiGlyph} ${className ?? ''}`} style={{ color, ...style }}>{icon}</span>
    : <svg aria-hidden="true" className={`${styles.glyph} ${className ?? ''}`} fill="currentColor" style={{ color, ...style }} viewBox="0 0 16 16"><use href="#CustomView"/></svg>
  return <svg aria-hidden="true" className={`${styles.glyph} ${className ?? ''}`} fill="currentColor" style={{ color, ...style }} viewBox="0 0 16 16"><use href={`#${assetIcon}`}/></svg>
}

export function ViewIconPicker({ align = 'start', ariaLabel, color = DEFAULT_VIEW_COLOR, icon = DEFAULT_VIEW_ICON, onChange, prependIcons = [], prependTeam = false, surface = 'card', triggerClassName, triggerContent }: { align?: 'start' | 'center' | 'end'; ariaLabel?: string; color?: string; icon?: string; onChange: (visual: ViewVisual) => void; prependIcons?: string[]; prependTeam?: boolean; /** The reference app tints the picker by elevation: `page` over a page (project, initiative, document), `card` over a settings card or dialog. */ surface?: 'page' | 'card'; triggerClassName?: string; /** Replaces the glyph inside the trigger (e.g. a loop template's icon). */ triggerContent?: ReactNode }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<'icons' | 'emojis'>('icons')
  const [query, setQuery] = useState('')
  const [draftColor, setDraftColor] = useState(normalizeColor(color))
  const searchRef = useRef<HTMLInputElement>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  const normalizedQuery = query.trim().toLowerCase()
  const filteredIcons = useMemo(() => {
    const leading = [...prependIcons, ...(prependTeam ? ['Team'] : [])]
    return [...leading, ...ICON_NAMES.filter(name => !leading.includes(name))].filter(name => name.toLowerCase().includes(normalizedQuery))
  }, [normalizedQuery, prependIcons, prependTeam])
  const emojiSections = useMemo(() => [
    { label: 'Frequently used', values: FREQUENT_EMOJIS.filter(value => matchesEmoji(value, normalizedQuery)) },
    { label: 'Smileys & People', values: PEOPLE_EMOJIS.filter(value => matchesEmoji(value, normalizedQuery)) },
  ].filter(section => section.values.length), [normalizedQuery])

  useEffect(() => { if (!open) { setQuery(''); setTab('icons') } }, [open])
  useEffect(() => setDraftColor(normalizeColor(color)), [color])

  const chooseColor = (nextColor: string) => {
    const normalized = normalizeColor(nextColor)
    setDraftColor(normalized)
    onChange({ icon, color: normalized })
  }
  const chooseIcon = (nextIcon: string) => { onChange({ icon: nextIcon, color: draftColor }); setOpen(false) }
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
    <Popover.Trigger asChild><button aria-label={ariaLabel ?? t('Choose icon')} className={`${styles.trigger} ${triggerClassName ?? ''}`} data-state={open ? 'open' : 'closed'} style={{ '--view-color': color } as CSSProperties} type="button">{triggerContent ?? <ViewGlyph color={color} icon={icon}/>}</button></Popover.Trigger>
    <Popover.Portal><Popover.Content data-flow-motion="floating" align={align} className={styles.content} data-surface={surface} collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => searchRef.current?.focus()) }} side="bottom" sideOffset={4}>
      <div aria-label={t('Icon type')} className={styles.tabs} role="tablist">
        <button aria-controls="view-icons-panel" aria-selected={tab === 'icons'} className={styles.tab} onClick={() => { setTab('icons'); setQuery(''); requestAnimationFrame(() => searchRef.current?.focus()) }} role="tab" type="button">{t('Icons')}</button>
        <button aria-controls="view-emojis-panel" aria-selected={tab === 'emojis'} className={styles.tab} onClick={() => { setTab('emojis'); setQuery(''); requestAnimationFrame(() => searchRef.current?.focus()) }} role="tab" type="button">{t('Emojis')}</button>
      </div>
      {tab === 'icons' && <div aria-label={t('Icons')} className={styles.panel} id="view-icons-panel" role="tabpanel">
        <ColorSwatchPicker className={styles.colorEditor} color={draftColor} onChange={chooseColor} onPreview={setDraftColor} surface={surface === 'page' ? 'popover' : 'icon'}/>
        <SearchBox icon={false} onArrowDown={() => focusGridItem(0)} placeholder={t('Search icons…')} query={query} searchRef={searchRef} setQuery={setQuery}/>
        <div className={styles.iconGrid} ref={gridRef}>{filteredIcons.map((name, index) => <button aria-label={name} className={styles.iconButton} data-i18n-ignore data-selected={icon === name} key={name} onClick={() => chooseIcon(name)} onKeyDown={event => moveGridFocus(event, index)} title={name} type="button"><ViewGlyph color={draftColor} icon={name}/></button>)}</div>
      </div>}
      {tab === 'emojis' && <div aria-label={t('Emojis')} className={`${styles.panel} ${styles.emojiPanel}`} id="view-emojis-panel" role="tabpanel">
        <SearchBox onArrowDown={() => focusGridItem(0)} placeholder={t('Search emoji…')} query={query} searchRef={searchRef} setQuery={setQuery}/>
        <div className={styles.emojiScroller} ref={gridRef}>{emojiSections.map(section => <section className={styles.emojiSection} key={section.label}><h3>{t(section.label)}</h3><div className={styles.emojiGrid}>{section.values.map((value, index) => <button aria-label={EMOJI_NAMES[value] ?? `emoji_${value.codePointAt(0)?.toString(16)}`} data-i18n-ignore data-selected={icon === value} key={`${section.label}:${value}:${index}`} onClick={() => chooseIcon(value)} onKeyDown={event => moveGridFocus(event, index)} type="button"><span>{value}</span></button>)}</div></section>)}</div>
      </div>}
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function SearchBox({ icon = true, onArrowDown, placeholder, query, searchRef, setQuery }: { icon?: boolean; onArrowDown: () => void; placeholder: string; query: string; searchRef: React.RefObject<HTMLInputElement | null>; setQuery: (query: string) => void }) {
  return <label className={styles.search}><input aria-label={placeholder} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'ArrowDown') { event.preventDefault(); onArrowDown() } }} placeholder={placeholder} ref={searchRef} value={query}/>{icon && <Search aria-hidden="true" size={13}/>}</label>
}

function matchesEmoji(value: string, query: string) { return !query || value.includes(query) || (EMOJI_NAMES[value] ?? '').includes(query.replaceAll(' ', '_')) }
function validColor(value: string) { return /^#[0-9a-f]{6}$/i.test(value) }
function isEmoji(value: string) { return /\p{Extended_Pictographic}/u.test(value) }
function normalizeColor(value: string) { return validColor(value) ? value.toLowerCase() : DEFAULT_VIEW_COLOR }
