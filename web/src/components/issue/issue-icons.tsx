import { useId, type SVGProps } from 'react'
import type { Cycle, WorkflowState } from '@/types/flow'

export type FlowIconProps = Omit<SVGProps<SVGSVGElement>, 'width' | 'height'> & { size?: number }

const priorityLabels = ['No Priority', 'Urgent Priority', 'High Priority', 'Medium Priority', 'Low Priority']

export function PriorityIcon({ priority, size = 15, className, ...props }: FlowIconProps & { priority: number }) {
  const normalized = Math.max(0, Math.min(4, priority))
  const classes = ['priority', `priority-${priority}`, className].filter(Boolean).join(' ')
  return <AssetIcon aria-label={priorityLabels[normalized]} asset={`Priority${normalized}`} className={classes} role="img" size={size} {...props} style={{ color: normalized === 1 ? 'lch(66% 80 48)' : undefined, ...props.style }}/>
}

function AssetIcon({ asset, size = 16, ...props }: FlowIconProps & { asset: string }) {
  const labelled = Boolean(props['aria-label'])
  return <svg aria-hidden={labelled ? undefined : true} fill="currentColor" focusable="false" height={size} role={labelled ? props.role ?? 'img' : undefined} viewBox="0 0 16 16" width={size} {...props}><use href={`#${asset}`}/></svg>
}

function FlowSvg({ size = 16, children, ...props }: FlowIconProps) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" role="img" focusable="false" aria-hidden="true" {...props}>{children}</svg>
}

export function NoAssigneeIcon(props: FlowIconProps) {
  return <FlowSvg {...props}>
    <path fillRule="evenodd" clipRule="evenodd" d="M10.25 6.75C10.25 7.99264 9.24264 9 8 9C6.75736 9 5.75 7.99264 5.75 6.75C5.75 5.50736 6.75736 4.5 8 4.5C9.24264 4.5 10.25 5.50736 10.25 6.75Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M8.5752 10C9.97242 10 11.2611 10.6106 12.1436 11.6143C12.1563 11.5997 12.17 11.586 12.1826 11.5713C12.4518 11.2567 12.9255 11.2202 13.2402 11.4893C13.5548 11.7585 13.5913 12.2321 13.3223 12.5469C13.0953 12.8123 12.8478 13.0593 12.584 13.2881C12.5484 13.3246 12.5106 13.3593 12.4668 13.3887C11.3913 14.2811 10.0437 14.8571 8.56738 14.9756C8.56118 14.9762 8.55508 14.978 8.54883 14.9785C8.51409 14.9812 8.4792 14.9822 8.44434 14.9844C8.38882 14.9879 8.3332 14.991 8.27734 14.9932C8.18529 14.9968 8.09287 15 8 15C7.90681 15 7.81406 14.9968 7.72168 14.9932C7.66583 14.991 7.6102 14.9879 7.55469 14.9844C7.52015 14.9822 7.48558 14.9812 7.45117 14.9785C7.44459 14.978 7.43816 14.9763 7.43164 14.9756C5.94988 14.8564 4.59683 14.2772 3.51953 13.3789C3.50616 13.3677 3.49384 13.3556 3.48145 13.3438C3.47213 13.3365 3.46218 13.33 3.45312 13.3223C3.17492 13.0844 2.91561 12.8251 2.67773 12.5469C2.40865 12.2321 2.44515 11.7585 2.75977 11.4893C3.07452 11.2202 3.54818 11.2567 3.81738 11.5713C3.83028 11.5864 3.84339 11.6013 3.85645 11.6162C4.73898 10.612 6.02721 10.0001 7.4248 10H8.5752ZM7.4248 11.5C6.47086 11.5001 5.59107 11.9168 4.9873 12.6016C5.85267 13.1696 6.88689 13.5 8 13.5C9.11327 13.5 10.1472 13.1687 11.0127 12.6006C10.4088 11.9164 9.52878 11.5 8.5752 11.5H7.4248Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M1.82715 6.76172C2.24007 6.79385 2.54868 7.15444 2.5166 7.56738C2.50553 7.70999 2.5 7.85427 2.5 8C2.5 8.14573 2.50553 8.29001 2.5166 8.43262C2.54868 8.84556 2.24007 9.20615 1.82715 9.23828C1.41418 9.27036 1.05357 8.9618 1.02148 8.54883C1.00741 8.36759 1 8.18457 1 8C1 7.81543 1.00741 7.63241 1.02148 7.45117C1.05357 7.0382 1.41418 6.72964 1.82715 6.76172Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M14.1729 6.76172C14.5858 6.72964 14.9464 7.0382 14.9785 7.45117C14.9926 7.63241 15 7.81543 15 8C15 8.18457 14.9926 8.36759 14.9785 8.54883C14.9464 8.9618 14.5858 9.27036 14.1729 9.23828C13.7599 9.20615 13.4513 8.84556 13.4834 8.43262C13.4945 8.29001 13.5 8.14573 13.5 8C13.5 7.85427 13.4945 7.70999 13.4834 7.56738C13.4513 7.15444 13.7599 6.79385 14.1729 6.76172Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M3.45312 2.67773C3.76789 2.40865 4.24155 2.44515 4.51074 2.75977C4.77982 3.07452 4.74329 3.54818 4.42871 3.81738C4.20954 4.00475 4.00475 4.20954 3.81738 4.42871C3.54818 4.74329 3.07452 4.77982 2.75977 4.51074C2.44515 4.24155 2.40865 3.76789 2.67773 3.45312C2.91561 3.17492 3.17492 2.91561 3.45312 2.67773ZM11.4893 2.75977C11.7585 2.44515 12.2321 2.40865 12.5469 2.67773C12.8251 2.91561 13.0844 3.17492 13.3223 3.45312C13.5913 3.76789 13.5548 4.24155 13.2402 4.51074C12.9255 4.77982 12.4518 4.74329 12.1826 4.42871C11.9953 4.20954 11.7905 4.00475 11.5713 3.81738C11.2567 3.54818 11.2202 3.07452 11.4893 2.75977ZM8 1C8.18457 1 8.36759 1.00741 8.54883 1.02148C8.9618 1.05357 9.27036 1.41418 9.23828 1.82715C9.20615 2.24007 8.84556 2.54868 8.43262 2.5166C8.1474 2.49446 7.8526 2.49446 7.56738 2.5166C7.15444 2.54868 6.79385 2.24007 6.76172 1.82715C6.72964 1.41418 7.0382 1.05357 7.45117 1.02148C7.63241 1.00741 7.81543 1 8 1Z"/>
  </FlowSvg>
}

export function NoProjectIcon(props: FlowIconProps) {
  return <FlowSvg {...props}><path fillRule="evenodd" clipRule="evenodd" d="M6.3201 1.71783C7.35318 1.09405 8.64683 1.09406 9.6799 1.71784L10.3674 2.13296L9.59206 3.41704L8.90456 3.00191C8.34829 2.66603 7.65171 2.66603 7.09543 3.00191L6.40794 3.41702L5.6326 2.13294L6.3201 1.71783ZM3.57011 3.37829L4.25761 2.96317L5.03294 4.24725L4.34545 4.66237C3.82071 4.9792 3.5 5.54749 3.5 6.16046V7.08003H2V6.16046C2 5.02209 2.59561 3.9667 3.57011 3.37829ZM11.7424 2.96321L12.4299 3.37834C13.4044 3.96675 14 5.02213 14 6.16049V7.08229H12.5V6.16049C12.5 5.54753 12.1793 4.97925 11.6546 4.66241L10.9671 4.24729L11.7424 2.96321ZM2 9.83874V8.91917H3.5V9.83874C3.5 10.4517 3.8207 11.02 4.34543 11.3368L5.03352 11.7523L4.25817 13.0364L3.57008 12.6209C2.59559 12.0325 2 10.9771 2 9.83874ZM14 8.92587V9.84767C14 10.9879 13.4025 12.0447 12.4254 12.6325L11.7385 13.0458L10.9652 11.7605L11.6521 11.3472C12.1782 11.0307 12.5 10.4616 12.5 9.84767V8.92587H14ZM6.32244 14.2828L5.63435 13.8673L6.4097 12.5833L7.09779 12.9988C7.6532 13.3341 8.34856 13.3347 8.90451 13.0002L9.59142 12.587L10.3647 13.8723L9.67778 14.2855C8.6453 14.9067 7.35391 14.9057 6.32244 14.2828Z"/></FlowSvg>
}

export function ProjectIcon(props: FlowIconProps) {
  return <FlowSvg {...props}><path fillRule="evenodd" clipRule="evenodd" d="M7.331 1.07a3.2 3.2 0 0 1 1.338 0c.498.106.967.377 1.904.917l1.354.78c.937.541 1.406.812 1.747 1.19.301.334.53.728.669 1.156.157.484.157 1.025.157 2.107v1.56l-.003.718c-.007.63-.036 1.026-.154 1.389l-.057.158a3.2 3.2 0 0 1-.612.998l-.135.138c-.33.312-.792.578-1.612 1.051l-1.354.78-.623.357c-.55.309-.907.481-1.281.56l-.166.032a3.2 3.2 0 0 1-1.006 0l-.166-.031c-.374-.08-.73-.252-1.281-.561l-.623-.356-1.354-.78c-.82-.474-1.281-.74-1.612-1.052l-.135-.138a3.2 3.2 0 0 1-.612-.998l-.057-.158c-.118-.363-.147-.758-.154-1.39L1.5 8.78V7.22c0-.946 0-1.479.105-1.921l.052-.186c.122-.374.312-.723.56-1.028l.11-.128c.255-.284.583-.507 1.126-.83l.62-.36 1.354-.78c.82-.473 1.281-.739 1.718-.869zM3 7.22v1.56c0 1.183.018 1.439.084 1.643l.064.167q.11.246.292.449l.059.06c.151.143.427.318 1.323.835l1.354.78.632.36c.188.104.33.178.442.233V8.482l-4.247-1.93zm5.75 1.262v4.826c.212-.106.533-.282 1.074-.594l1.354-.78.628-.368c.499-.297.646-.407.754-.527l.113-.14q.158-.218.243-.476l.022-.081c.035-.144.051-.351.058-.835L13 8.78V7.22l-.004-.668zM7.82 2.51l-.177.027c-.159.034-.328.106-.835.39l-.632.359-1.354.78c-.896.517-1.172.692-1.323.834l-.059.06q-.046.051-.086.104l4.645 2.112 4.645-2.112-.084-.103c-.109-.12-.255-.23-.754-.528l-.628-.367-1.354-.78c-.897-.517-1.186-.668-1.386-.728l-.08-.021a1.7 1.7 0 0 0-.538-.027"/></FlowSvg>
}

export function LabelIcon(props: FlowIconProps) {
  return <AssetIcon asset="Label" {...props}/>
}

export function AddReactionIcon(props: FlowIconProps) {
  return <FlowSvg {...props}><path d="M8 1a7 7 0 0 1 7 7 .75.75 0 0 1-1.5 0A5.5 5.5 0 1 0 8 13.5.75.75 0 0 1 8 15 7 7 0 1 1 8 1Zm4.25 8.5a.75.75 0 0 1 .743.648l.007.102v1.249l1.25.001a.75.75 0 0 1 .743.648l.007.102a.75.75 0 0 1-.648.743L14.25 13 13 12.999v1.251a.75.75 0 0 1-1.493.102l-.007-.102v-1.251L10.25 13a.75.75 0 0 1-.743-.648L9.5 12.25a.75.75 0 0 1 .648-.743l.102-.007 1.25-.001V10.25a.75.75 0 0 1 .75-.75ZM10.475 8a.5.5 0 0 1 .497.553C10.798 10.184 9.812 11 8.016 11c-1.795 0-2.789-.814-2.982-2.441a.5.5 0 0 1 .438-.556l.03-.002L10.475 8ZM6 5a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2Z"/></FlowSvg>
}

export function AttachmentIcon(props: FlowIconProps) {
  return <FlowSvg {...props}><path d="M12.6429 7.69048L8.92925 11.4041C7.48164 12.8517 5.34347 13.0101 4.16667 11.8333C2.98733 10.654 3.14447 8.52219 4.59216 7.07451L8.00206 3.66461C8.93557 2.73109 10.2976 2.63095 11.0333 3.36667C11.7681 4.10139 11.6658 5.4675 10.7361 6.39727L7.32363 9.8097C6.90202 10.2313 6.32171 10.2741 6.02381 9.97619C5.72651 9.6789 5.76949 9.09718 6.1989 8.66776L9.29048 5.57619C9.56662 5.30005 9.56662 4.85233 9.29048 4.57619C9.01433 4.30005 8.56662 4.30005 8.29048 4.57619L5.1989 7.66776C4.24737 8.6193 4.13865 10.091 5.02381 10.9762C5.9095 11.8619 7.37984 11.7535 8.32363 10.8097L11.7361 7.39727C13.1876 5.94573 13.3564 3.68975 12.0333 2.36667C10.7099 1.04326 8.45782 1.20884 7.00206 2.66461L3.59216 6.07451C1.62229 8.04437 1.39955 11.0662 3.16667 12.8333C4.93146 14.5981 7.9596 14.3737 9.92925 12.4041L13.6429 8.69048C13.919 8.41433 13.919 7.96662 13.6429 7.69048C13.3667 7.41433 12.919 7.41433 12.6429 7.69048Z"/></FlowSvg>
}

export function CycleIcon({ cycle, noCycle = false, nextUpcomingId, progress = 0, size = 16, className, ...props }: FlowIconProps & { cycle?: Cycle; noCycle?: boolean; nextUpcomingId?: string; progress?: number }) {
  const future = cycle?.status === 'upcoming' && Boolean(nextUpcomingId && cycle.id !== nextUpcomingId)
  const classes = ['cycle-icon', cycle?.status === 'current' && 'cycle-progress-icon', future && 'cycle-future-icon', className].filter(Boolean).join(' ')
  const play = <path stroke="none" d="M6.95588 5.28329 10.6901 7.43926c.3334.19245.3334.67357 0 .86602l-3.73422 2.15592c-.33333.1925-.75-.0481-.75-.433V5.71631c0-.3849.41667-.62547.75-.43302Z"/>
  const dotted = <circle cx="8" cy="8" fill="none" r="6.25" stroke="currentColor" strokeDasharray="1.636246173744684" strokeDashoffset=".818123086872342" strokeWidth="1.5"/>
  if (noCycle) return <svg aria-hidden="true" className={classes} fill="none" focusable="false" height={size} viewBox="0 0 16 16" width={size} {...props}>{dotted}</svg>
  if (cycle?.status === 'current') {
    const circumference = 39.269908169872416
    const clamped = Math.max(0, Math.min(100, progress))
    const progressOffset = circumference + .642455968958294 - circumference * clamped / 100
    return <svg aria-hidden="true" className={classes} fill="currentColor" focusable="false" height={size} viewBox="0 0 16 16" width={size} {...props}>
      <circle className="cycle-progress-icon__track" cx="8" cy="8" fill="none" r="6.25" strokeDasharray={`${circumference}px`} strokeDashoffset="5.357544031041707px" strokeLinecap="round" strokeWidth="1.5" style={{ transformBox: 'fill-box', transition: 'transform .6s, stroke-dashoffset .6s' }} transform="rotate(-54.26075591453578 6.25 6.25)"/>
      <circle className="cycle-progress-icon__value" cx="8" cy="8" fill="none" r="6.25" strokeDasharray={`${circumference}px`} strokeDashoffset={`${progressOffset}px`} strokeLinecap="round" strokeWidth="1.5" style={{ transformBox: 'fill-box', transition: 'stroke-dashoffset .6s' }} transform="rotate(-76 6.25 6.25)"/>
      {play}
    </svg>
  }
  if (future) return <svg aria-hidden="true" className={classes} fill="currentColor" focusable="false" height={size} viewBox="0 0 16 16" width={size} {...props}>{dotted}{play}</svg>
  if (cycle?.status === 'upcoming') return <svg aria-hidden="true" className={classes} fill="currentColor" focusable="false" height={size} viewBox="0 0 16 16" width={size} {...props}><circle cx="8" cy="8" fill="none" r="6.25" stroke="currentColor" strokeWidth="1.5"/>{dotted}{play}</svg>
  return <svg aria-hidden="true" className={classes} fill="currentColor" focusable="false" height={size} viewBox="0 0 16 16" width={size} {...props}>
    <path d="M8 1a.75.75 0 0 1 0 1.5A5.5 5.5 0 1 0 13.5 8a5.48 5.48 0 0 0-2.123-4.341.75.75 0 1 1 .923-1.183A7 7 0 1 1 8 1Z"/>{play}
  </svg>
}

export function LabelGroupIcon({ size = 16, color = 'currentColor', ...props }: FlowIconProps & { color?: string }) {
  return <svg aria-hidden="true" width={size} height={size} viewBox="0 0 16 16" fill={color} focusable="false" {...props}>
    <path fillRule="evenodd" clipRule="evenodd" d="M7.95021 6C8.91671 6 9.70021 5.2165 9.70021 4.25C9.70021 3.2835 8.91671 2.5 7.95021 2.5C6.98371 2.5 6.20021 3.2835 6.20021 4.25C6.20021 5.2165 6.98371 6 7.95021 6ZM4.4502 9.5C5.41669 9.5 6.2002 8.7165 6.2002 7.75C6.2002 6.7835 5.41669 6 4.4502 6C3.4837 6 2.7002 6.7835 2.7002 7.75C2.7002 8.7165 3.48369 9.5 4.4502 9.5ZM7.95018 13C8.91668 13 9.70018 12.2165 9.70018 11.25C9.70018 10.2835 8.91668 9.5 7.95018 9.5C6.98369 9.5 6.20018 10.2835 6.20018 11.25C6.20018 12.2165 6.98369 13 7.95018 13ZM13.2002 7.75C13.2002 8.7165 12.4167 9.5 11.4502 9.5C10.4837 9.5 9.7002 8.7165 9.7002 7.75C9.7002 6.7835 10.4837 6 11.4502 6C12.4167 6 13.2002 6.7835 13.2002 7.75Z"/>
  </svg>
}

export function TeamIcon(props: FlowIconProps) {
  return <AssetIcon asset="Team" {...props}/>
}

export function MembersIcon(props: FlowIconProps) {
  return <AssetIcon asset="Members" {...props}/>
}

export function CalendarIcon({ variant = 'target', ...props }: FlowIconProps & { variant?: 'start'|'target' }) {
  return <AssetIcon asset={variant === 'start' ? 'StartDate' : 'TargetDate'} {...props}/>
}

export function SlackIcon(props: FlowIconProps) {
  return <AssetIcon asset="Slack" {...props}/>
}

type ProjectStatusKind = 'backlog'|'planned'|'started'|'completed'|'canceled'

const projectStatusColors: Record<ProjectStatusKind, string> = {
  backlog: 'lch(67.969% 62.082 61.651)',
  planned: 'lch(67.969% 1.608 272.005)',
  started: 'lch(80% 90 85)',
  completed: 'lch(48% 59.31 288.43)',
  canceled: '#8A8F98',
}

export function ProjectStatusIcon({ color, name, progress, type, size = 16, ...props }: FlowIconProps & { color?: string; name?: string; progress?: number; type?: string }) {
  const kind = projectStatusKind(type, name)
  const maskId = `project-status-${useId().replaceAll(':', '')}-${kind}`
  const stroke = color ?? projectStatusColors[kind]
  const statusProgress = kind === 'started'
    ? progress === undefined ? .5 : Math.max(.15, Math.min(.85, progress * .7 + .15))
    : kind === 'completed' || kind === 'canceled' ? 1 : 0
  const progressLength = statusProgress * 25.12
  const finished = kind === 'completed' || kind === 'canceled'
  const label = props['aria-label'] ?? (name ? `${name} status` : undefined)
  return <svg aria-hidden={label ? undefined : true} fill="none" focusable="false" height={size} role={label ? props.role ?? 'img' : undefined} viewBox="-1 -1 16 16" width={size} {...props} aria-label={label}>
    <path d="M2.95778 3.02069L5.70777 1.36023C6.50244 0.88041 7.49756 0.88041 8.29223 1.36024L11.0422 3.02074C11.7918 3.47336 12.25 4.2852 12.25 5.16086V8.84803C12.25 9.7251 11.7904 10.5381 11.0388 10.9902L8.29114 12.6433C7.49693 13.1211 6.50355 13.1203 5.71011 12.6412L2.95775 10.9792C2.20815 10.5266 1.75 9.7148 1.75 8.83911V5.16082C1.75 4.28516 2.20816 3.47332 2.95778 3.02069Z" fill="none" stroke={stroke} strokeDasharray={kind === 'backlog' ? '1.65 1.35' : '3.14 0'} strokeDashoffset={kind === 'backlog' ? 2.3 : 1} strokeLinejoin="bevel" strokeWidth="1.5"/>
    <g mask={`url(#${maskId})`}><circle cx="7" cy="7" fill="none" r="4" stroke={stroke} strokeDasharray={`${progressLength} 25.12`} strokeWidth="8" style={{ transition: 'stroke-dasharray 160ms cubic-bezier(.2,.8,.2,1)' }} transform="rotate(-90 7 7)"/></g>
    <mask id={maskId} maskUnits="userSpaceOnUse">
      <path d="M8.3779 4.74233C8.14438 4.60607 7.85562 4.60607 7.6221 4.74233L5.37209 6.05513C5.14168 6.18957 5 6.4363 5 6.70311V9.34216C5 9.60897 5.14168 9.85573 5.37209 9.99016L7.6221 11.303C7.85562 11.4392 8.14438 11.4392 8.3779 11.303L10.6279 9.99016C10.8583 9.85573 11 9.60897 11 9.34216V6.70311C11 6.4363 10.8583 6.18957 10.6279 6.05513L8.3779 4.74233Z" fill="white" transform={finished ? 'translate(-7.5, -7.5) scale(1.8)' : 'translate(-1, -1)'}/>
      {kind === 'completed' && <path d="M10.7803 5.28033C11.0732 4.98744 11.0732 4.51256 10.7803 4.21967C10.4874 3.92678 10.0126 3.92678 9.7197 4.21967L5.75 8.18934L4.28033 6.71967C3.98744 6.42678 3.51256 6.42678 3.21967 6.71967C2.92678 7.01256 2.92678 7.48744 3.21967 7.78033L5.21967 9.7803C5.51256 10.0732 5.98744 10.0732 6.28033 9.7803L10.7803 5.28033Z" fill="black"/>}
      {kind === 'canceled' && <path d="M3.73657 3.73657C4.05199 3.42114 4.56339 3.42114 4.87881 3.73657L7 5.85775L9.12117 3.73657C9.4366 3.42114 9.94801 3.42114 10.2634 3.73657C10.5789 4.05199 10.5789 4.56339 10.2634 4.87881L8.14225 7L10.2634 9.12118C10.5789 9.4366 10.5789 9.94801 10.2634 10.2634C9.94801 10.5789 9.4366 10.5789 9.12117 10.2634L7 8.14225L4.87881 10.2634C4.56339 10.5789 4.05199 10.5789 3.73657 10.2634C3.42114 9.94801 3.42114 9.4366 3.73657 9.12118L5.85775 7L3.73657 4.87881C3.42114 4.56339 3.42114 4.05199 3.73657 3.73657Z" fill="black"/>}
    </mask>
  </svg>
}

function projectStatusKind(type?: string, name?: string): ProjectStatusKind {
  const normalized = `${type ?? ''} ${name ?? ''}`.toLowerCase()
  if (normalized.includes('backlog')) return 'backlog'
  if (normalized.includes('progress') || normalized.includes('started')) return 'started'
  if (normalized.includes('complete')) return 'completed'
  if (normalized.includes('cancel')) return 'canceled'
  return 'planned'
}

export function StatusIcon({state,size=15}:{state:Pick<WorkflowState,'id'|'name'|'color'|'type'>;size?:number}){
  const name=state.name.toLowerCase(),id=state.id.toLowerCase(),duplicate=id.includes('duplicate')||name==='duplicate'
  const terminalColor=(canonical:boolean)=>canonical?'var(--issue-status-terminal)':state.color
  if(duplicate)return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true" fill={terminalColor(id==='state_duplicate'||name==='duplicate')}><path fillRule="evenodd" clipRule="evenodd" d="M7 14C10.866 14 14 10.866 14 7C14 3.13401 10.866 0 7 0C3.13401 0 0 3.13401 0 7C0 10.866 3.13401 14 7 14ZM9.5791 5.71973C9.872 5.42684 10.3468 5.42686 10.6396 5.71973C10.9325 6.01262 10.9325 6.48738 10.6396 6.78027L6.78027 10.6396C6.48738 10.9325 6.01262 10.9325 5.71973 10.6396C5.42686 10.3468 5.42684 9.872 5.71973 9.5791L9.5791 5.71973ZM7.21973 3.36035C7.51261 3.06746 7.98738 3.06747 8.28027 3.36035C8.57315 3.65325 8.57316 4.12801 8.28027 4.4209L4.4209 8.28027C4.12801 8.57316 3.65325 8.57315 3.36035 8.28027C3.06747 7.98738 3.06746 7.51261 3.36035 7.21973L7.21973 3.36035Z"/></svg>
  if(state.type==='canceled')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true" fill={terminalColor(id==='state_canceled'||name==='canceled')}><path fillRule="evenodd" clipRule="evenodd" d="M7 14C10.866 14 14 10.866 14 7C14 3.13401 10.866 0 7 0C3.13401 0 0 3.13401 0 7C0 10.866 3.13401 14 7 14ZM5.03033 3.96967C4.73744 3.67678 4.26256 3.67678 3.96967 3.96967C3.67678 4.26256 3.67678 4.73744 3.96967 5.03033L5.93934 7L3.96967 8.96967C3.67678 9.26256 3.67678 9.73744 3.96967 10.0303C4.26256 10.3232 4.73744 10.3232 5.03033 10.0303L7 8.06066L8.96967 10.0303C9.26256 10.3232 9.73744 10.3232 10.0303 10.0303C10.3232 9.73744 10.3232 9.26256 10.0303 8.96967L8.06066 7L10.0303 5.03033C10.3232 4.73744 10.3232 4.26256 10.0303 3.96967C9.73744 3.67678 9.26256 3.67678 8.96967 3.96967L7 5.93934L5.03033 3.96967Z"/></svg>
  if(state.type==='completed')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true" fill={id==='state_done'||name==='done'?'var(--issue-status-completed)':state.color}><path fillRule="evenodd" clipRule="evenodd" d="M7 0C3.13401 0 0 3.13401 0 7C0 10.866 3.13401 14 7 14C10.866 14 14 10.866 14 7C14 3.13401 10.866 0 7 0ZM11.101 5.10104C11.433 4.76909 11.433 4.23091 11.101 3.89896C10.7691 3.56701 10.2309 3.56701 9.89896 3.89896L5.5 8.29792L4.10104 6.89896C3.7691 6.56701 3.2309 6.56701 2.89896 6.89896C2.56701 7.2309 2.56701 7.7691 2.89896 8.10104L4.89896 10.101C5.2309 10.433 5.7691 10.433 6.10104 10.101L11.101 5.10104Z"/></svg>
  if(state.type==='started')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><rect x="1" y="1" width="12" height="12" rx="6" fill="none" stroke={state.color} strokeWidth="1.5"/><path fill={state.color} stroke="none" d={name.includes('review')?'M 3.5,3.5 L3.5,0 A3.5,3.5 0 1,1 0, 3.5 z':'M 3.5,3.5 L3.5,0 A3.5,3.5 0 0,1 3.5, 7 z'} transform="translate(3.5,3.5)"/></svg>
  if(state.type==='backlog')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true" fill={state.color}><path d="M13.9408 7.91426L11.9576 7.65557C11.9855 7.4419 12 7.22314 12 7C12 6.77686 11.9855 6.5581 11.9576 6.34443L13.9408 6.08573C13.9799 6.38496 14 6.69013 14 7C14 7.30987 13.9799 7.61504 13.9408 7.91426ZM13.4688 4.32049C13.2328 3.7514 12.9239 3.22019 12.5538 2.73851L10.968 3.95716C11.2328 4.30185 11.4533 4.68119 11.6214 5.08659L13.4688 4.32049ZM11.2615 1.4462L10.0428 3.03204C9.69815 2.76716 9.31881 2.54673 8.91341 2.37862L9.67951 0.531163C10.2486 0.767153 10.7798 1.07605 11.2615 1.4462ZM7.91426 0.0591659L7.65557 2.04237C7.4419 2.01449 7.22314 2 7 2C6.77686 2 6.5581 2.01449 6.34443 2.04237L6.08574 0.059166C6.38496 0.0201343 6.69013 0 7 0C7.30987 0 7.61504 0.0201343 7.91426 0.0591659ZM4.32049 0.531164L5.08659 2.37862C4.68119 2.54673 4.30185 2.76716 3.95716 3.03204L2.73851 1.4462C3.22019 1.07605 3.7514 0.767153 4.32049 0.531164ZM1.4462 2.73851L3.03204 3.95716C2.76716 4.30185 2.54673 4.68119 2.37862 5.08659L0.531164 4.32049C0.767153 3.7514 1.07605 3.22019 1.4462 2.73851ZM0.0591659 6.08574C0.0201343 6.38496 0 6.69013 0 7C0 7.30987 0.0201343 7.61504 0.059166 7.91426L2.04237 7.65557C2.01449 7.4419 2 7.22314 2 7C2 6.77686 2.01449 6.5581 2.04237 6.34443L0.0591659 6.08574ZM0.531164 9.67951L2.37862 8.91341C2.54673 9.31881 2.76716 9.69815 3.03204 10.0428L1.4462 11.2615C1.07605 10.7798 0.767153 10.2486 0.531164 9.67951ZM2.73851 12.5538L3.95716 10.968C4.30185 11.2328 4.68119 11.4533 5.08659 11.6214L4.32049 13.4688C3.7514 13.2328 3.22019 12.9239 2.73851 12.5538ZM6.08574 13.9408L6.34443 11.9576C6.5581 11.9855 6.77686 12 7 12C7.22314 12 7.4419 11.9855 7.65557 11.9576L7.91427 13.9408C7.61504 13.9799 7.30987 14 7 14C6.69013 14 6.38496 13.9799 6.08574 13.9408ZM9.67951 13.4688L8.91341 11.6214C9.31881 11.4533 9.69815 11.2328 10.0428 10.968L11.2615 12.5538C10.7798 12.9239 10.2486 13.2328 9.67951 13.4688ZM12.5538 11.2615L10.968 10.0428C11.2328 9.69815 11.4533 9.31881 11.6214 8.91341L13.4688 9.67951C13.2328 10.2486 12.924 10.7798 12.5538 11.2615Z"/></svg>
  return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><rect x="1" y="1" width="12" height="12" rx="6" fill="none" stroke={state.color} strokeWidth="1.5"/></svg>
}
