import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Dialog from '@radix-ui/react-dialog'
import { Check, ChevronRight, Filter, SlidersHorizontal, X } from 'lucide-react'
import { useState } from 'react'
import type { BootstrapData } from '@/types/flow'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { filterValues, pulseFilterLabels, type PulseFilter, type PulseFilterField, type PulseFilterMatch } from './pulse-model'

const groups: PulseFilterField[][] = [
  ['author','team','createdDate'],
  ['updateType','health'],
  ['initiative'],
  ['project','projectMember','projectStatus','projectLabel'],
]

export function PulseFilterMenu({ data, filters, match, onChange, onMatchChange }: { data:BootstrapData;filters:PulseFilter[];match:PulseFilterMatch;onChange:(filters:PulseFilter[])=>void;onMatchChange:(match:PulseFilterMatch)=>void }) {
  const [query,setQuery]=useState('')
  const [advancedOpen,setAdvancedOpen]=useState(false)
  const visibleGroups=groups.map(fields=>fields.filter(field=>pulseFilterLabels[field].toLowerCase().includes(query.trim().toLowerCase()))).filter(fields=>fields.length)
  const updateField=(field:PulseFilterField,value:string)=>{const existing=filters.find(filter=>filter.field===field);if(existing){const values=existing.values.includes(value)?existing.values.filter(item=>item!==value):[...existing.values,value];onChange(values.length?filters.map(filter=>filter.id===existing.id?{...filter,values}:filter):filters.filter(filter=>filter.id!==existing.id));return}onChange([...filters,{id:`pulse_filter_${crypto.randomUUID()}`,field,operator:'is',values:[value]}])}
  return <><DropdownMenu.Root onOpenChange={open=>{if(!open)setQuery('')}}><DropdownMenu.Trigger asChild><button className="pulse-filter-button" type="button"><Filter size={13}/>Add filter</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="pulse-menu pulse-filter-builder" sideOffset={5}>
    <div className="pulse-menu-search"><input aria-label="Add Filter…" autoFocus placeholder="Add Filter…" value={query} onChange={event=>setQuery(event.target.value)}/><kbd>F</kbd></div>
    {!query&&<><DropdownMenu.Item onSelect={()=>setAdvancedOpen(true)}><SlidersHorizontal size={14}/><span>Advanced filter</span></DropdownMenu.Item><DropdownMenu.Separator/></>}
    {visibleGroups.map((fields,index)=><div key={fields.join(':')}>{index>0&&<DropdownMenu.Separator/>}{fields.map(field=><FilterFieldSubmenu data={data} field={field} filters={filters} key={field} onToggle={value=>updateField(field,value)}/>)}</div>)}
    {!visibleGroups.length&&<div className="pulse-filter-empty">No filters found</div>}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  <PulseAdvancedFilters filters={filters} match={match} onChange={onChange} onMatchChange={onMatchChange} open={advancedOpen} onOpenChange={setAdvancedOpen}/></>
}

function FilterFieldSubmenu({data,field,filters,onToggle}:{data:BootstrapData;field:PulseFilterField;filters:PulseFilter[];onToggle:(value:string)=>void}){
  const [query,setQuery]=useState('')
  const selected=new Set(filters.find(filter=>filter.field===field)?.values??[])
  const values=filterValues(data,field).filter(value=>value.label.toLowerCase().includes(query.trim().toLowerCase()))
  return <DropdownMenu.Sub onOpenChange={open=>{if(!open)setQuery('')}}><DropdownMenu.SubTrigger><span>{pulseFilterLabels[field]}</span><ChevronRight className="pulse-menu-end" size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="pulse-menu pulse-filter-values" sideOffset={4} alignOffset={-5}><div className="pulse-menu-search"><input aria-label={pulseFilterLabels[field]} autoFocus placeholder={pulseFilterLabels[field]} value={query} onChange={event=>setQuery(event.target.value)}/></div>{values.map(value=><DropdownMenu.Item key={value.id} onSelect={event=>{event.preventDefault();onToggle(value.id)}}><span className="pulse-filter-checkbox">{selected.has(value.id)&&<CheckboxMark/>}</span><span data-i18n-ignore>{value.label}</span></DropdownMenu.Item>)}{!values.length&&<div className="pulse-filter-empty">No results</div>}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
}

export function PulseFilterChips({data,filters,onChange}:{data:BootstrapData;filters:PulseFilter[];onChange:(filters:PulseFilter[])=>void}){
  return <div className="pulse-filter-chips">{filters.map(filter=>{const labels=filter.values.map(value=>filterValues(data,filter.field).find(option=>option.id===value)?.label??value);return <span key={filter.id}><b>{pulseFilterLabels[filter.field]}</b><em>{filter.operator==='isNot'?'is not':'is'}</em><strong data-i18n-ignore>{labels.join(', ')}</strong><button aria-label={`Remove ${pulseFilterLabels[filter.field]} filter`} onClick={()=>onChange(filters.filter(item=>item.id!==filter.id))} type="button"><X size={11}/></button></span>})}</div>
}

function PulseAdvancedFilters({filters,match,onChange,onMatchChange,open,onOpenChange}:{filters:PulseFilter[];match:PulseFilterMatch;onChange:(filters:PulseFilter[])=>void;onMatchChange:(match:PulseFilterMatch)=>void;open:boolean;onOpenChange:(open:boolean)=>void}){
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="pulse-advanced-overlay"/><Dialog.Content aria-describedby={undefined} className="pulse-advanced-filter"><header><Dialog.Title>Advanced filter</Dialog.Title><Dialog.Close asChild><button aria-label="Close advanced filter"><X size={14}/></button></Dialog.Close></header><label><span>Match</span><div><button aria-pressed={match==='all'} onClick={()=>onMatchChange('all')}>All filters</button><button aria-pressed={match==='any'} onClick={()=>onMatchChange('any')}>Any filter</button></div></label>{filters.length?<div className="pulse-advanced-rows">{filters.map(filter=><div key={filter.id}><Filter size={13}/><span>{pulseFilterLabels[filter.field]}</span><button className="pulse-filter-operator" aria-label={`Change ${pulseFilterLabels[filter.field]} operator`} onClick={()=>onChange(filters.map(item=>item.id===filter.id?{...item,operator:item.operator==='is'?'isNot':'is'}:item))}>{filter.operator==='isNot'?'is not':'is'}</button><b>{filter.values.length} selected</b><button aria-label={`Remove ${pulseFilterLabels[filter.field]} filter`} onClick={()=>onChange(filters.filter(item=>item.id!==filter.id))}><X size={12}/></button></div>)}</div>:<p>No filters added yet.</p>}<footer><button onClick={()=>onChange([])} disabled={!filters.length}>Clear all</button><Dialog.Close asChild><button className="is-primary">Done</button></Dialog.Close></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

export function PulseMatchSummary({match,count}:{match:PulseFilterMatch;count:number}){return count>1?<span className="pulse-match-summary"><Check size={11}/>{match==='all'?'Match all':'Match any'}</span>:null}
