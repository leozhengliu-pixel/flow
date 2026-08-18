import { useId, type SVGProps } from 'react'
import type { WorkflowState } from '@/types/flow'

export type FlowIconProps = Omit<SVGProps<SVGSVGElement>, 'width' | 'height'> & { size?: number }

const priorityLabels = ['No Priority', 'Urgent Priority', 'High Priority', 'Medium Priority', 'Low Priority']

export function PriorityIcon({ priority, size = 15, className, ...props }: FlowIconProps & { priority: number }) {
  const normalized = Math.max(0, Math.min(4, priority))
  const classes = ['priority', `priority-${priority}`, className].filter(Boolean).join(' ')
  return <AssetIcon aria-label={priorityLabels[normalized]} asset={`Priority${normalized}`} className={classes} role="img" size={size} {...props} style={{ color: normalized === 1 ? 'lch(66% 80 48)' : undefined, ...props.style }}/>
}

function AssetIcon({ asset, size = 16, source = 'property', ...props }: FlowIconProps & { asset: string; source?: 'core'|'property' }) {
  const labelled = Boolean(props['aria-label'])
  return <svg aria-hidden={labelled ? undefined : true} fill="currentColor" focusable="false" height={size} role={labelled ? props.role ?? 'img' : undefined} viewBox="0 0 16 16" width={size} {...props}><use href={`/${source === 'core' ? 'flow-core-icons' : 'flow-property-icons'}.svg#${asset}`}/></svg>
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
  return <AssetIcon asset="Label" source="core" {...props}/>
}

export function TeamIcon(props: FlowIconProps) {
  return <AssetIcon asset="Team" source="core" {...props}/>
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

export function ProjectStatusIcon({ color, name, type, size = 16, ...props }: FlowIconProps & { color?: string; name?: string; type?: string }) {
  const kind = projectStatusKind(type, name)
  const maskId = `project-status-${useId().replaceAll(':', '')}-${kind}`
  const standard = ({ backlog: 'Backlog', planned: 'Planned', started: 'In Progress', completed: 'Completed', canceled: 'Canceled' } as const)[kind]
  const stroke = name === standard || !color ? projectStatusColors[kind] : color
  const progress = kind === 'started' ? 12.56 : kind === 'completed' || kind === 'canceled' ? 25.12 : 0
  const finished = kind === 'completed' || kind === 'canceled'
  const label = props['aria-label'] ?? (name ? `${name} status` : undefined)
  return <svg aria-hidden={label ? undefined : true} fill="none" focusable="false" height={size} role={label ? props.role ?? 'img' : undefined} viewBox="-1 -1 16 16" width={size} {...props} aria-label={label}>
    <path d="M2.95778 3.02069L5.70777 1.36023C6.50244 0.88041 7.49756 0.88041 8.29223 1.36024L11.0422 3.02074C11.7918 3.47336 12.25 4.2852 12.25 5.16086V8.84803C12.25 9.7251 11.7904 10.5381 11.0388 10.9902L8.29114 12.6433C7.49693 13.1211 6.50355 13.1203 5.71011 12.6412L2.95775 10.9792C2.20815 10.5266 1.75 9.7148 1.75 8.83911V5.16082C1.75 4.28516 2.20816 3.47332 2.95778 3.02069Z" fill="none" stroke={stroke} strokeDasharray={kind === 'backlog' ? '1.65 1.35' : '3.14 0'} strokeDashoffset={kind === 'backlog' ? 2.3 : 1} strokeLinejoin="bevel" strokeWidth="1.5"/>
    <g mask={`url(#${maskId})`}><circle cx="7" cy="7" fill="none" r="4" stroke={stroke} strokeDasharray={`${progress} 25.12`} strokeWidth="8" transform="rotate(-90 7 7)"/></g>
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
  const duplicate=state.id.toLowerCase().includes('duplicate')||state.name.toLowerCase()==='duplicate'
  if(duplicate)return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="6.25" fill={state.color}/><path d="m4.1 7.15 3.05-3.05m-.3 5.8L9.9 6.85" stroke="#25272b" strokeLinecap="round" strokeWidth="1.55"/></svg>
  if(state.type==='canceled')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="6.25" fill={state.color}/><path d="m4.55 4.55 4.9 4.9m0-4.9-4.9 4.9" stroke="#25272b" strokeLinecap="round" strokeWidth="1.5"/></svg>
  if(state.type==='completed')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="6.25" fill={state.color}/><path d="m3.75 7.2 2.05 2.05 4.45-4.5" fill="none" stroke="#25272b" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.55"/></svg>
  if(state.type==='started')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="5.75" fill="none" stroke={state.color} strokeWidth="1.5"/><path d="M7 1.25a5.75 5.75 0 0 1 0 11.5Z" fill={state.color}/></svg>
  if(state.type==='backlog')return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="5.75" fill="none" stroke={state.color} strokeDasharray="1.25 1.8" strokeLinecap="round" strokeWidth="1.8"/></svg>
  return <svg width={size} height={size} viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="5.75" fill="none" stroke={state.color} strokeWidth="1.5"/></svg>
}
