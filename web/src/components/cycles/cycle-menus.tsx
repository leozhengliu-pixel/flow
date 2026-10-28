import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { CalendarDays, ChevronRight, Copy, Download, Edit3, ExternalLink, Link2, MoreHorizontal, Play, Star } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from '@/lib/toast'

import type { Cycle, CycleMutationInput, User } from '@/types/flow'
import { getCycleCalendarFeed } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { SyncedMentionField } from '@/components/ui/synced-mention-field'
import { formatEstimatePoints } from '@/lib/estimates'
import { canStartCycleToday, cycleDateBounds, cycleDateMoveAffects } from './cycle-model'

/** `capacityUnit`: what the team's capacity counts — estimate points, or issues when the team does not use estimates. */
/**
 * The cycle's options menu. `cycles` is the team's cycles: they decide whether "Start cycle
 * today…" is offered (the next upcoming cycle while no cycle is current) and how far a date can
 * move. A current cycle's start does not move, and there is no action that completes a cycle: it
 * ends after its last day ("Move end date…" changes that day).
 */
export function CycleActions({cycle,cycles,onUpdate,onStart,onReload,canonicalPath,detail=false,members=[],capacityUnit='points'}:{cycle:Cycle;cycles?:Cycle[];onUpdate:(input:CycleMutationInput)=>Promise<unknown>;onStart?:()=>Promise<unknown>;onReload:()=>Promise<void>;canonicalPath?:string;detail?:boolean;members?:User[];capacityUnit?:'points'|'issues'}){
  const {t}=useI18n(),[editor,setEditor]=useState<'details'|'start'|'end'>(),[confirm,setConfirm]=useState(false)
  const teamCycles=cycles??[cycle],startToday=Boolean(onStart)&&canStartCycleToday(teamCycles,cycle)
  const feed=async(action:'copy'|'download'|'google')=>{try{const result=await getCycleCalendarFeed(cycle.id),absolute=new URL(result.url,location.origin).toString();if(action==='copy'){await navigator.clipboard.writeText(absolute);toast.success(t('Calendar feed URL copied'))}else if(action==='download'){const anchor=document.createElement('a');anchor.href=absolute;anchor.download=`cycle-${cycle.number}.ics`;anchor.click()}else window.open(`https://calendar.google.com/calendar/render?action=TEMPLATE&text=${encodeURIComponent(cycle.name)}&dates=${dateForGoogle(cycle.startsAt)}/${dateForGoogle(cycle.endsAt)}`,'_blank')}catch(error){toast.error(error instanceof Error?error.message:t('Could not create calendar feed'))}}
  return <><DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="cycle-row-menu" aria-label={t(detail?'Cycle options':'Open menu')}><MoreHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" className="flow-cycle-menu" align="end" sideOffset={5} collisionPadding={10}><DropdownMenu.Item onSelect={()=>setEditor('details')}><Edit3/><span>{t('Edit cycle name and description…')}</span></DropdownMenu.Item><DropdownMenu.Sub><DropdownMenu.SubTrigger><CalendarDays/><span>{t('Change cycle dates')}</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className="flow-cycle-menu" sideOffset={4}>{cycle.status!=='current'&&<DropdownMenu.Item onSelect={()=>setEditor('start')}>{t('Move start date…')}</DropdownMenu.Item>}<DropdownMenu.Item onSelect={()=>setEditor('end')}>{t('Move end date…')}</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>{startToday&&<DropdownMenu.Item onSelect={()=>setConfirm(true)}><Play/><span>{t('Start cycle today…')}</span></DropdownMenu.Item>}<DropdownMenu.Separator/><DropdownMenu.Item onSelect={()=>void onUpdate({favorite:!cycle.favorite})}><Star/><span>{t(cycle.favorite?'Unfavorite':'Favorite')}</span></DropdownMenu.Item><DropdownMenu.Item onSelect={()=>void navigator.clipboard.writeText(new URL(canonicalPath??location.pathname,location.origin).toString())}><Copy/><span>{t('Copy link')}</span></DropdownMenu.Item><DropdownMenu.Sub><DropdownMenu.SubTrigger><CalendarDays/><span>{t('Subscribe to cycle calendar')}</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className="flow-cycle-menu cycle-calendar-menu" sideOffset={4}><DropdownMenu.Item onSelect={()=>void feed('google')}><ExternalLink/>{t('Add to Google Calendar')}</DropdownMenu.Item><DropdownMenu.Item onSelect={()=>void feed('copy')}><Link2/>{t('Copy feed URL for your calendar')}</DropdownMenu.Item><DropdownMenu.Item onSelect={()=>void feed('download')}><Download/>{t('Download .ics calendar file')}</DropdownMenu.Item></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root><CycleEditor cycle={cycle} cycles={teamCycles} members={members} capacityUnit={capacityUnit} mode={editor} open={Boolean(editor)} onOpenChange={open=>!open&&setEditor(undefined)} onSubmit={async input=>{await onUpdate(input);await onReload()}}/><CycleConfirm title={t('Start {name} today?').replace('{name}',cycle.name)} open={confirm} onOpenChange={setConfirm} onConfirm={async()=>{await onStart?.();setConfirm(false)}}><p>{t('The upcoming cycle’s start date will be moved to today.')}</p></CycleConfirm></>
}

/** A date move that changes a neighbouring cycle, or any upcoming start move, is confirmed first. */
type PendingMove={mode:'start'|'end';input:CycleMutationInput;neighbor?:Cycle}

function CycleEditor({cycle,cycles,members,capacityUnit,mode,open,onOpenChange,onSubmit}:{cycle:Cycle;cycles:Cycle[];members:User[];capacityUnit:'points'|'issues';mode?:'details'|'start'|'end';open:boolean;onOpenChange:(open:boolean)=>void;onSubmit:(input:CycleMutationInput)=>Promise<void>}){
  const [pending,setPending]=useState<PendingMove>()
  const scheduled=cycle.status!=='completed',bounds:{min?:string;max?:string}=scheduled&&mode&&mode!=='details'?cycleDateBounds(cycles,cycle,mode):{}
  const{t}=useI18n(),[name,setName]=useState(cycle.name),[description,setDescription]=useState(cycle.description),[capacity,setCapacity]=useState(overrideText(cycle)),[startsAt,setStartsAt]=useState(cycle.startsAt.slice(0,10)),[endsAt,setEndsAt]=useState(cycle.endsAt.slice(0,10)),[saving,setSaving]=useState(false)
  const [matrix,setMatrix]=useState<Record<string,Record<string,number>>>(cycle.capacityByMember ?? {})
  // The form restarts from the cycle whenever the cycle changes or the dialog opens or closes.
  const [synced,setSynced]=useState({cycle,open})
  if(synced.cycle!==cycle||synced.open!==open){setSynced({cycle,open});setName(cycle.name);setDescription(cycle.description);setCapacity(overrideText(cycle));setMatrix(cycle.capacityByMember ?? {});setStartsAt(cycle.startsAt.slice(0,10));setEndsAt(cycle.endsAt.slice(0,10))}
  const automatic=t(capacityUnit==='issues'?'Automatic ({count} issues)':'Automatic ({count} points)').replace('{count}',formatEstimatePoints(cycle.capacityAutomatic??cycle.capacity))
  const save=async()=>{
    if(scheduled&&(mode==='start'||mode==='end')){
      const value=mode==='start'?startsAt:endsAt,neighbor=cycleDateMoveAffects(cycles,cycle,mode,value)
      if(value===(mode==='start'?cycle.startsAt:cycle.endsAt).slice(0,10)){onOpenChange(false);return}
      if(mode==='start'||neighbor){setPending({mode,input:mode==='start'?{startsAt}:{endsAt},neighbor});onOpenChange(false);return}
    }
    setSaving(true);try{await onSubmit(mode==='details'?{name:name.trim(),description:description.trim(),...capacityChange(cycle,capacity),capacityByMember:matrix}:mode==='start'?{startsAt}:{endsAt});onOpenChange(false)}finally{setSaving(false)}
  }
  const outOfBounds=(value:string)=>Boolean(bounds.min&&value<bounds.min||bounds.max&&value>bounds.max)
  const invalidDates=mode==='start'?scheduled?outOfBounds(startsAt):startsAt>=endsAt:mode==='end'?outOfBounds(endsAt)||endsAt<=startsAt:false
  return <><Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="cycle-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className={`flow-cycle-dialog ${mode==='details'?'edit':'date'}`}><Dialog.Title>{t(mode==='details'?'Edit cycle':mode==='start'?'Change cycle start date':'Change cycle end date')}</Dialog.Title><Dialog.Close aria-label={t('Discard')}>×</Dialog.Close>{mode==='details'?<><label>{t('Cycle name')}<input autoFocus value={name} onChange={event=>setName(event.target.value)}/></label><label>{t('Cycle description')}<SyncedMentionField ariaLabel={t('Cycle description')} className="cycle-description-field" placeholder={t('Add description…')} value={description} onChange={setDescription}/></label><label className="cycle-capacity-field">{t('Cycle capacity')}<span><input inputMode="numeric" min="1" type="number" placeholder={automatic} value={capacity} onChange={event=>setCapacity(event.target.value)}/>{capacity.trim()!==''&&<button type="button" onClick={()=>setCapacity('')}>{t('Use automatic')}</button>}</span><small>{capacity.trim()===''?t(cycle.capacitySource==='default'?'Until the team completes a cycle, {count} per member.':cycle.capacitySource==='velocity'?'Average completed in the team\'s last completed cycles (up to three).':'Saving returns the cycle to the automatic capacity.').replace('{count}',String(CAPACITY_PER_MEMBER)):automatic}</small></label>{members.length>0&&<fieldset className="cycle-capacity-matrix"><legend>{t('Capacity by member and day')}</legend><div className="cycle-capacity-matrix__scroll"><table><thead><tr><th>{t('Member')}</th>{['mon','tue','wed','thu','fri','sat','sun'].map(day=><th key={day}>{day.slice(0,1).toUpperCase()+day.slice(1)}</th>)}</tr></thead><tbody>{members.map(member=><tr key={member.id}><th>{member.displayName}</th>{['mon','tue','wed','thu','fri','sat','sun'].map(day=><td key={day}><input aria-label={`${member.displayName} ${day} capacity`} min="0" max="24" type="number" value={matrix[member.id]?.[day] ?? 0} onChange={event=>setMatrix(current=>({...current,[member.id]:{...(current[member.id]??{}),[day]:Math.max(0,Math.min(24,Number(event.target.value)||0))}}))}/></td>)}</tr>)}</tbody></table></div></fieldset>}</>:<label>{t(mode==='start'?'Start date':'End date')}<DateTimeControl label={t(mode==='start'?'Start date':'End date')} min={bounds.min} max={bounds.max} value={mode==='start'?startsAt:endsAt} onChange={value=>mode==='start'?setStartsAt(value):setEndsAt(value)}/></label>}<footer><Dialog.Close>{t('Cancel')}</Dialog.Close><button className="primary" disabled={saving||mode==='details'&&!name.trim()||invalidDates} onClick={()=>void save()}>{saving?t('Saving…'):t(mode==='details'?'Save':'Apply')}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>{pending&&<CycleMoveConfirm cycle={cycle} move={pending} onOpenChange={open=>!open&&setPending(undefined)} onConfirm={async input=>{await onSubmit(input);setPending(undefined)}}/>}</>
}

/**
 * Confirms a date move. A later end that reaches the next cycle moves its start; an upcoming
 * cycle's start moves with this cycle only (the end stays) or with the future cycles (the length
 * stays and every later cycle moves too), and an earlier start that reaches the previous cycle
 * moves its end.
 */
function CycleMoveConfirm({cycle,move,onOpenChange,onConfirm}:{cycle:Cycle;move:PendingMove;onOpenChange:(open:boolean)=>void;onConfirm:(input:CycleMutationInput)=>Promise<void>}){
  const{t}=useI18n(),[shiftFollowing,setShiftFollowing]=useState(false)
  const start=move.mode==='start'
  return <CycleConfirm title={t(start?'Change start date for {name}':'Change end date for {name}').replace('{name}',cycle.name)} open onOpenChange={onOpenChange} onConfirm={()=>onConfirm(start?{...move.input,shiftFollowing}:move.input)}>
    {start?<>{move.neighbor&&<p>{t('The end date of {name} will be moved to accommodate this cycle’s new start date.').replace('{name}',move.neighbor.name)}</p>}<p>{t('How should this change affect future cycles?')}</p><div className="cycle-date-choices" role="radiogroup" aria-label={t('How should this change affect future cycles?')}><label><input type="radio" name="cycle-date-choice" checked={!shiftFollowing} onChange={()=>setShiftFollowing(false)}/>{t('Change this cycle’s start date only')}</label><label><input type="radio" name="cycle-date-choice" checked={shiftFollowing} onChange={()=>setShiftFollowing(true)}/>{t('Change this and future cycle start dates')}</label></div></>:<p>{t('The next cycle’s start date will be moved to accommodate this change.')}</p>}
  </CycleConfirm>
}

function CycleConfirm({title,open,onOpenChange,onConfirm,children}:{title:string;open:boolean;onOpenChange:(open:boolean)=>void;onConfirm:()=>Promise<void>;children:ReactNode}){const{t}=useI18n(),[saving,setSaving]=useState(false);return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="cycle-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="flow-cycle-dialog confirm"><Dialog.Title data-i18n-ignore>{title}</Dialog.Title>{children}<footer><Dialog.Close>{t('Cancel')}</Dialog.Close><button className="primary" disabled={saving} onClick={()=>{setSaving(true);void onConfirm().finally(()=>setSaving(false))}}>{t('Confirm')}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>}
/** Mirrors the server's default capacity per active team member (api/cmd/server/cycle_capacity.go). */
const CAPACITY_PER_MEMBER=30
function overrideText(cycle:Cycle){return cycle.capacityOverride!=null?String(cycle.capacityOverride):''}
/** The capacity part of an Edit cycle save: an override, null to return to automatic, or nothing when unchanged. */
function capacityChange(cycle:Pick<Cycle,'capacityOverride'>,text:string):Pick<CycleMutationInput,'capacity'>{
  const value=Math.round(Number(text.trim()))
  const next=text.trim()===''||!Number.isFinite(value)||value<1?null:value
  return next===(cycle.capacityOverride??null)?{}:{capacity:next}
}
function dateForGoogle(value:string){return new Date(value).toISOString().slice(0,10).replaceAll('-','')}
