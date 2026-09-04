import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Search } from 'lucide-react'
import { FLOW_CORE_ICON_NAMES, FLOW_VIEW_ICON_ALIASES, FLOW_VIEW_ICON_NAMES } from './flow-view-icon-data'
import styles from './view-icon-picker.module.css'
import { useI18n } from '@/i18n/i18n'

export const DEFAULT_VIEW_ICON = 'CustomView'
export const DEFAULT_VIEW_COLOR = '#bec2c8'

const ICON_NAMES: readonly string[] = FLOW_VIEW_ICON_NAMES
const ICON_NAME_SET = new Set<string>(ICON_NAMES)

const FREQUENT_EMOJIS = `👍 👌 🙏 😂 ❤️ 👀 ✅ 🙂 😃 😄 😀 🤔 😅 ⚠️ 😕 ❌ 🙌 🎉 😉 😊 🤷 👋 ❓`.split(' ')
const PEOPLE_EMOJIS = `😀 😃 😄 😁 😆 😅 🤣 😂 🙂 🙃 🫠 😉 😊 😇 🥰 😍 🤩 😘 😗 ☺️ 😚 😙 🥲 😋 😛 😜 🤪 😝 🤑 🤗 🤭 🫢 🫣 🤫 🤔 🫡 🤐 🤨 😐 😑 😶 😶‍🌫️ 😏 😒 🙄 😬 😮‍💨 🤥 😌 😔 😪 🤤 😴 😷 🤒 🤕 🤢 🤮 🤧 🥵 🥶 🥴 😵 😵‍💫 🤯 🤠 🥳 🥸 😎 🤓 🧐 😕 🫤 😟 🙁 ☹️ 😮 😯 😲 😳 🥺 🥹 😦 😧 😨 😰 😥 😢 😭 😱 😖 😣 😞 😓 😩 😫 🥱 😤 😡 😠 🤬 😈 👿 💀 ☠️ 💩 🤡 👻 👽 👾 🤖 👋 🤚 🖐️ ✋ 🖖 🫱 🫲 🫳 🫴 👌 🤌 🤏 ✌️ 🤞 🫰 🤟 🤘 🤙 👈 👉 👆 🖕 👇 ☝️ 🫵 👍 👎 ✊ 👊 🤛 🤜 👏 🙌 🫶 👐 🤲 🤝 🙏 ✍️ 💅`.split(' ')
const PRESET_COLORS = ['#95a2b3', '#5e6ad2', '#24b4c7', '#4cb782', '#f2c300', '#eb9138', '#c99790', '#ee565d']
const EMOJI_NAMES: Record<string, string> = { '👍': '+1', '👌': 'ok_hand', '🙏': 'pray', '😂': 'joy', '❤️': 'heart', '👀': 'eyes', '✅': 'white_check_mark', '🙂': 'slightly_smiling_face', '⚠️': 'warning', '❌': 'x', '🙌': 'raised_hands', '🎉': 'tada', '🤷': 'shrug', '👋': 'wave', '❓': 'question' }

export type ViewVisual = { icon: string; color: string }

export function ViewGlyph({ className, color = DEFAULT_VIEW_COLOR, icon = DEFAULT_VIEW_ICON, style }: { className?: string; color?: string; icon?: string; style?: CSSProperties }) {
  const assetIcon = FLOW_VIEW_ICON_ALIASES[icon] ?? icon
  if (!ICON_NAME_SET.has(assetIcon) && assetIcon !== 'Team') return isEmoji(icon)
    ? <span aria-hidden="true" className={`${styles.glyph} ${styles.emojiGlyph} ${className ?? ''}`} style={{ color, ...style }}>{icon}</span>
    : <svg aria-hidden="true" className={`${styles.glyph} ${className ?? ''}`} fill="currentColor" style={{ color, ...style }} viewBox="0 0 16 16"><use href="#CustomView"/></svg>
  return <svg aria-hidden="true" className={`${styles.glyph} ${className ?? ''}`} fill="currentColor" style={{ color, ...style }} viewBox="0 0 16 16"><use href={`${FLOW_CORE_ICON_NAMES.has(assetIcon) ? '' : '/flow-view-icons.svg'}#${assetIcon}`}/></svg>
}

export function ViewIconPicker({ align = 'start', ariaLabel, color = DEFAULT_VIEW_COLOR, icon = DEFAULT_VIEW_ICON, onChange, prependIcons = [], prependTeam = false, triggerClassName }: { align?: 'start' | 'center' | 'end'; ariaLabel?: string; color?: string; icon?: string; onChange: (visual: ViewVisual) => void; prependIcons?: string[]; prependTeam?: boolean; triggerClassName?: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [tab, setTab] = useState<'icons' | 'emojis'>('icons')
  const [query, setQuery] = useState('')
  const [customColorOpen, setCustomColorOpen] = useState(false)
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

  useEffect(() => { if (!open) { setQuery(''); setTab('icons'); setCustomColorOpen(false) } }, [open])
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
    <Popover.Trigger asChild><button aria-label={ariaLabel ?? t('Choose icon')} className={`${styles.trigger} ${triggerClassName ?? ''}`} data-state={open ? 'open' : 'closed'} style={{ '--view-color': color } as CSSProperties} type="button"><ViewGlyph color={color} icon={icon}/></button></Popover.Trigger>
    <Popover.Portal><Popover.Content align={align} className={styles.content} collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => searchRef.current?.focus()) }} side="bottom" sideOffset={4}>
      <div aria-label={t('Icon type')} className={styles.tabs} role="tablist">
        <button aria-controls="view-icons-panel" aria-selected={tab === 'icons'} className={styles.tab} onClick={() => { setTab('icons'); setQuery(''); requestAnimationFrame(() => searchRef.current?.focus()) }} role="tab" type="button">{t('Icons')}</button>
        <button aria-controls="view-emojis-panel" aria-selected={tab === 'emojis'} className={styles.tab} onClick={() => { setTab('emojis'); setQuery(''); requestAnimationFrame(() => searchRef.current?.focus()) }} role="tab" type="button">{t('Emojis')}</button>
      </div>
      {tab === 'icons' && <div aria-label={t('Icons')} className={`${styles.panel} ${customColorOpen ? '' : styles.compactPanel}`} id="view-icons-panel" role="tabpanel">
        <ColorEditor color={draftColor} custom={customColorOpen} onCustom={() => setCustomColorOpen(true)} onChange={setDraftColor} onCommit={chooseColor}/>
        <SearchBox onArrowDown={() => focusGridItem(0)} placeholder={t('Search icons…')} query={query} searchRef={searchRef} setQuery={setQuery}/>
        <div className={`${styles.iconGrid} ${customColorOpen ? '' : styles.compactIconGrid}`} ref={gridRef}>{filteredIcons.map((name, index) => <button aria-label={name} className={styles.iconButton} data-i18n-ignore data-selected={icon === name} key={name} onClick={() => chooseIcon(name)} onKeyDown={event => moveGridFocus(event, index)} title={name} type="button"><ViewGlyph color={draftColor} icon={name}/></button>)}</div>
      </div>}
      {tab === 'emojis' && <div aria-label={t('Emojis')} className={`${styles.panel} ${styles.emojiPanel}`} id="view-emojis-panel" role="tabpanel">
        <SearchBox onArrowDown={() => focusGridItem(0)} placeholder={t('Search emoji…')} query={query} searchRef={searchRef} setQuery={setQuery}/>
        <div className={styles.emojiScroller} ref={gridRef}>{emojiSections.map(section => <section className={styles.emojiSection} key={section.label}><h3>{t(section.label)}</h3><div className={styles.emojiGrid}>{section.values.map((value, index) => <button aria-label={EMOJI_NAMES[value] ?? `emoji_${value.codePointAt(0)?.toString(16)}`} data-i18n-ignore data-selected={icon === value} key={`${section.label}:${value}:${index}`} onClick={() => chooseIcon(value)} onKeyDown={event => moveGridFocus(event, index)} type="button"><span>{value}</span></button>)}</div></section>)}</div>
      </div>}
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function ColorEditor({ color, custom, onCustom, onChange, onCommit }: { color: string; custom: boolean; onCustom: () => void; onChange: (color: string) => void; onCommit: (color: string) => void }) {
  const { t } = useI18n()
  const hsv = useMemo(() => hexToHsv(color), [color])
  const [hexDraft, setHexDraft] = useState(color)
  useEffect(() => setHexDraft(color), [color])
  const updateSaturation = (event: PointerEvent<HTMLDivElement>, commit = false) => {
    const rect = event.currentTarget.getBoundingClientRect()
    const saturation = clamp((event.clientX - rect.left) / rect.width)
    const value = 1 - clamp((event.clientY - rect.top) / rect.height)
    const next = hsvToHex(hsv.h, saturation, value)
    onChange(next)
    if (commit) onCommit(next)
  }
  const updateHue = (event: PointerEvent<HTMLDivElement>, commit = false) => {
    const rect = event.currentTarget.getBoundingClientRect()
    const next = hsvToHex(clamp((event.clientY - rect.top) / rect.height) * 360, hsv.s, hsv.v)
    onChange(next)
    if (commit) onCommit(next)
  }
  const commitHex = (value: string) => {
    if (validColor(value)) onCommit(value)
    else setHexDraft(color)
  }
  if (!custom) return <div className={`${styles.colorEditor} ${styles.compactColorEditor}`}>
    <div className={styles.colorPalette}>
      <button aria-label={`${t('Selected color')} ${color}`} className={styles.selectedColor} style={{ backgroundColor: color }} type="button"><CheckMark/></button>
      {PRESET_COLORS.map(value => <button aria-label={`${t('Color')} ${value}`} data-selected={value === color} key={value} onClick={() => onCommit(value)} style={{ '--palette-color': value } as CSSProperties} type="button"/>)}
      <span className={styles.paletteDivider}/>
      <button aria-label={t('Set custom color')} className={styles.paletteCustom} onClick={onCustom} type="button"/>
    </div>
  </div>
  return <div className={styles.colorEditor}>
    <div className={styles.colorHeader}>
      <button aria-label={`${t('Selected color')} ${color}`} className={styles.selectedColor} style={{ backgroundColor: color }} type="button"><CheckMark/></button>
      <label><span>HEX</span><input aria-label={t('HEX color')} maxLength={7} onBlur={event => commitHex(event.currentTarget.value)} onChange={event => { const next = event.target.value; setHexDraft(next); if (validColor(next)) onChange(next) }} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); commitHex(event.currentTarget.value) } }} value={hexDraft}/></label>
      <button aria-label={t('Select default color')} className={styles.defaultColor} onClick={() => onCommit(DEFAULT_VIEW_COLOR)} type="button"><span/></button>
    </div>
    <div className={styles.colorControls}>
      <div aria-label={t('Saturation and brightness')} aria-valuemax={100} aria-valuemin={0} aria-valuenow={Math.round(hsv.s * 100)} className={styles.saturation} onKeyDown={event => { if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) return; event.preventDefault(); const saturation = clamp(hsv.s + (event.key === 'ArrowRight' ? .02 : event.key === 'ArrowLeft' ? -.02 : 0)); const value = clamp(hsv.v + (event.key === 'ArrowUp' ? .02 : event.key === 'ArrowDown' ? -.02 : 0)); onCommit(hsvToHex(hsv.h, saturation, value)) }} onPointerDown={event => { event.currentTarget.setPointerCapture(event.pointerId); updateSaturation(event) }} onPointerMove={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) updateSaturation(event) }} onPointerUp={event => { updateSaturation(event, true); event.currentTarget.releasePointerCapture(event.pointerId) }} role="slider" style={{ '--picker-hue': `hsl(${hsv.h} 100% 50%)` } as CSSProperties} tabIndex={0}><span style={{ left: `${hsv.s * 100}%`, top: `${(1 - hsv.v) * 100}%` }}/></div>
      <div aria-label={t('Hue')} className={styles.hue} onPointerDown={event => { event.currentTarget.setPointerCapture(event.pointerId); updateHue(event) }} onPointerMove={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) updateHue(event) }} onPointerUp={event => { updateHue(event, true); event.currentTarget.releasePointerCapture(event.pointerId) }} role="slider" aria-valuemin={0} aria-valuemax={360} aria-valuenow={Math.round(hsv.h)} tabIndex={0} onKeyDown={event => { if (!['ArrowUp', 'ArrowDown'].includes(event.key)) return; event.preventDefault(); const nextHue = (hsv.h + (event.key === 'ArrowDown' ? 5 : -5) + 360) % 360; onCommit(hsvToHex(nextHue, hsv.s, hsv.v)) }}><span style={{ top: `${hsv.h / 360 * 100}%` }}/></div>
    </div>
  </div>
}

function SearchBox({ onArrowDown, placeholder, query, searchRef, setQuery }: { onArrowDown: () => void; placeholder: string; query: string; searchRef: React.RefObject<HTMLInputElement | null>; setQuery: (query: string) => void }) {
  return <label className={styles.search}><input aria-label={placeholder} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'ArrowDown') { event.preventDefault(); onArrowDown() } }} placeholder={placeholder} ref={searchRef} value={query}/><Search aria-hidden="true" size={13}/></label>
}

function CheckMark() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 10 8"><path d="M3.47 5.708 1.884 4.123a.576.576 0 0 0-.815.814l1.996 1.994a.576.576 0 0 0 .814 0L8.931 1.883a.576.576 0 0 0-.815-.814L3.47 5.708Z"/></svg> }
function matchesEmoji(value: string, query: string) { return !query || value.includes(query) || (EMOJI_NAMES[value] ?? '').includes(query.replaceAll(' ', '_')) }
function validColor(value: string) { return /^#[0-9a-f]{6}$/i.test(value) }
function isEmoji(value: string) { return /\p{Extended_Pictographic}/u.test(value) }
function normalizeColor(value: string) { return validColor(value) ? value.toLowerCase() : DEFAULT_VIEW_COLOR }
function clamp(value: number) { return Math.min(1, Math.max(0, value)) }
function hexToHsv(hex: string) {
  const normalized = normalizeColor(hex).slice(1)
  const [r, g, b] = [0, 2, 4].map(index => Number.parseInt(normalized.slice(index, index + 2), 16) / 255)
  const max = Math.max(r, g, b), min = Math.min(r, g, b), delta = max - min
  let h = 0
  if (delta) h = max === r ? 60 * (((g - b) / delta) % 6) : max === g ? 60 * ((b - r) / delta + 2) : 60 * ((r - g) / delta + 4)
  return { h: (h + 360) % 360, s: max ? delta / max : 0, v: max }
}
function hsvToHex(h: number, s: number, v: number) {
  const chroma = v * s, x = chroma * (1 - Math.abs((h / 60) % 2 - 1)), m = v - chroma
  const [r, g, b] = h < 60 ? [chroma, x, 0] : h < 120 ? [x, chroma, 0] : h < 180 ? [0, chroma, x] : h < 240 ? [0, x, chroma] : h < 300 ? [x, 0, chroma] : [chroma, 0, x]
  return `#${[r, g, b].map(value => Math.round((value + m) * 255).toString(16).padStart(2, '0')).join('')}`
}
