import { useState } from 'react'
import { subDays, subMonths, subYears } from 'date-fns'
import type { Team, User } from '@/types/flow'
import { Calendar, Check, ChevronRight, Filter, SlidersHorizontal, UserRound, X } from 'lucide-react'
import { PropertyMenu } from '@/components/property/property-menu'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import { StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { FilterConditionChip } from '@/components/filter/applied-filter-bar'
import { usePeopleDirectory } from '@/components/property/people-context'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { SelectControl } from '@/components/ui/select-control'
import { SearchDateFilter } from './search-date-filter'
import type { DateMode } from '@/components/projects-page/project-target-date-picker'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuLabel, DropdownMenuSub, DropdownMenuSubTrigger, DropdownMenuSubContent, DropdownMenuCheckboxItem } from '@/components/ui/dropdown-menu'
import { DisplayIcon, FilterIcon } from '@/components/ui/view-action-icons'
import { useI18n } from '@/i18n/i18n'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { searchFields, searchDateLabel, isSearchConditionValid, searchConditionValues, teamSelection, type SearchPageState, type SearchFilterField, type SearchCondition, type SearchOrder } from './search-state'

const searchStatuses = [{id:'backlog',label:'Backlog'},{id:'unstarted',label:'Unstarted'},{id:'started',label:'Started'},{id:'completed',label:'Completed'},{id:'canceled',label:'Canceled'}] as const
const orderOptions: Array<{id:SearchOrder;label:string}> = [{id:'relevance',label:'Most relevant'},{id:'updatedAt',label:'Last updated'},{id:'createdAt',label:'Last created'},{id:'title',label:'Title'}]

export function SearchMenus({state,onChange,users,teams=[]}:{state:SearchPageState;onChange:(state:SearchPageState)=>void;users?:User[];teams?:Team[]}) {
  const {t}=useI18n()
  const [filterOpen,setFilterOpen]=useState(false)
  const [filterField,setFilterField]=useState<SearchFilterField>()
  const [advancedOpen,setAdvancedOpen]=useState(false)
  const [dateField,setDateField]=useState<SearchFilterField>()
  const [advanced,setAdvanced]=useState(state)
  const teamFilter=state.filters.find(filter=>filter.field==='team')
  const closeFilters=()=>{setFilterField(undefined);setFilterOpen(false)}
  const add=(field:SearchFilterField,value:string,operator:SearchCondition['operator']='is',granularity?:DateMode)=>{
    onChange({...state,filters:[...state.filters,{id:crypto.randomUUID(),field,value,operator,...(granularity?{granularity}:{})}]})
    closeFilters()
  }
  return <>
    <DropdownMenu open={filterOpen} onOpenChange={open=>{setFilterOpen(open);if(!open)setFilterField(undefined)}}>
      <DropdownMenuTrigger asChild><button className="workspace-search-tool" type="button" aria-label={t('Add filter')} title={t('Add filter')}><FilterIcon/></button></DropdownMenuTrigger>
      <DropdownMenuContent className="workspace-search-menu" align="end" aria-label={t('Search filters')}>
        <DropdownMenuItem className="workspace-search-advanced-option" onSelect={()=>{setAdvanced(state);setAdvancedOpen(true)}}><SlidersHorizontal size={15}/>{t('Advanced filter')}</DropdownMenuItem>
        <DropdownMenuSeparator/>
        {searchFields.map(field=><DropdownMenuSub key={field.id} open={filterField===field.id} onOpenChange={open=>setFilterField(open?field.id:undefined)}>
          <DropdownMenuSubTrigger>{field.id==='team'?<TeamIcon/>:field.id.endsWith('At')?<Calendar size={15}/>:field.id==='statusType'?<Filter size={15}/>:<UserRound size={15}/>}<span>{t(field.label)}</span></DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="workspace-search-value-menu" aria-label={t(field.label)} onKeyDown={event=>event.stopPropagation()}>
            {field.id==='team'?<div className="workspace-search-filter-values"><SearchTeamValue teams={teams} onClose={closeFilters} values={teamFilter?searchConditionValues(teamFilter):[]} onChange={values=>{
              const next=teamSelection(teamFilter??{id:crypto.randomUUID(),field:'team',operator:'is',value:''},values)
              onChange({...state,filters:teamFilter?state.filters.flatMap(filter=>filter.id===teamFilter.id?(values.length?[next]:[]):[filter]):values.length?[...state.filters,next]:state.filters})
            }}/></div>:field.id.endsWith('At') ? <>
              {searchDatePresets().map(preset=><DropdownMenuItem key={preset.label} onSelect={()=>add(field.id,preset.value,'after','day')}>{t(preset.label)}</DropdownMenuItem>)}
              <DropdownMenuSeparator/>
              <DropdownMenuItem onSelect={()=>{closeFilters();setDateField(field.id)}}>{t('Custom date or timeframe…')}</DropdownMenuItem>
            </> : <div className="workspace-search-filter-values"><SearchFilterValue users={users} field={field.id} value="" onCancel={closeFilters} onChange={(value,operator,granularity)=>add(field.id,value,operator,granularity)}/></div>}
          </DropdownMenuSubContent>
        </DropdownMenuSub>)}
      </DropdownMenuContent>
    </DropdownMenu>
    <Dialog open={Boolean(dateField)} onOpenChange={open=>{if(!open)setDateField(undefined)}}><DialogContent className="search-date-dialog" data-dialog-placement="window" aria-describedby={undefined} closeLabel={t('Close')}>
      <DialogTitle className="sr-only">{t(searchFields.find(field=>field.id===dateField)?.label??'Updated date')}</DialogTitle>
      {dateField && <SearchDateFilter label={t(searchFields.find(field=>field.id===dateField)!.label)} onCancel={()=>setDateField(undefined)} onApply={(value,operator,granularity)=>{add(dateField,value,operator,granularity);setDateField(undefined)}}/>}
    </DialogContent></Dialog>
    <DropdownMenu>
      <DropdownMenuTrigger asChild><button className="workspace-search-tool" type="button" aria-label={t('Display options')} title={t('Display options')}><DisplayIcon/></button></DropdownMenuTrigger>
      <DropdownMenuContent className="workspace-search-menu" align="end" aria-label={t('Search display options')}>
        <DropdownMenuSub><DropdownMenuSubTrigger><span>{t('Ordering')}</span><small>{t(orderOptions.find(item=>item.id===state.order)!.label)}</small></DropdownMenuSubTrigger><DropdownMenuSubContent>{orderOptions.map(item=><DropdownMenuItem key={item.id} onSelect={()=>onChange({...state,order:item.id})} role="menuitemradio" aria-checked={state.order===item.id}>{t(item.label)}{state.order===item.id&&<Check size={14}/>}</DropdownMenuItem>)}</DropdownMenuSubContent></DropdownMenuSub>
        <DropdownMenuCheckboxItem checked={state.includeArchived} onCheckedChange={includeArchived=>onChange({...state,includeArchived:includeArchived===true})}>{t('Include archived')}</DropdownMenuCheckboxItem>
        <DropdownMenuSeparator/>
        <DropdownMenuLabel>{t('Display properties')}</DropdownMenuLabel>
        <DropdownMenuCheckboxItem checked={state.showId} onCheckedChange={showId=>onChange({...state,showId:showId===true})}>{t('ID')}</DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
    <Dialog open={advancedOpen} onOpenChange={setAdvancedOpen}><DialogContent closeLabel={t('Close')} className="workspace-search-advanced" aria-describedby={undefined}><DialogTitle>{t('Advanced filter')}</DialogTitle>
      <SelectControl label={t('Match filters')} value={advanced.match} options={[{value:'and',label:t('All filters')},{value:'or',label:t('Any filter')}]} onChange={match=>setAdvanced({...advanced,match:match as 'and'|'or'})}/>
      <div className="workspace-search-conditions">{advanced.filters.map(condition=><div className="workspace-search-condition" key={condition.id}>
        <SelectControl label={t('Filter field')} value={condition.field} options={searchFields.map(field=>({value:field.id,label:t(field.label)}))} onChange={field=>setAdvanced({...advanced,filters:advanced.filters.map(item=>item.id===condition.id?{...item,field:field as SearchFilterField,value:'',values:undefined,operator:field.endsWith('At')?'after':'is'}:item)})}/>
        <SelectControl label={t('Filter operator')} value={condition.operator} options={(condition.field==='team'?['is','isNot','in','notIn']:condition.field.endsWith('At')?['on','after','before']:['is','isNot']).map(operator=>({value:operator,label:t(searchOperatorLabel(operator))}))} onChange={operator=>setAdvanced({...advanced,filters:advanced.filters.map(item=>item.id===condition.id?{...item,operator:operator as SearchCondition['operator']}:item)})}/>
        {condition.field==='team'?<SearchTeamValue teams={teams} compact values={searchConditionValues(condition)} onChange={values=>setAdvanced({...advanced,filters:advanced.filters.map(item=>item.id===condition.id?{...item,value:values[0]??'',values}:item)})}/>:<SearchFilterValue users={users} field={condition.field} value={condition.value} compact onChange={(value,_operator,granularity)=>setAdvanced({...advanced,filters:advanced.filters.map(item=>item.id===condition.id?{...item,value,...(granularity?{granularity}:{})}:item)})}/>}
        <button type="button" aria-label={t('Remove filter')} onClick={()=>setAdvanced({...advanced,filters:advanced.filters.filter(item=>item.id!==condition.id)})}><X size={14}/></button>
      </div>)}</div>
      <footer><button type="button" onClick={()=>setAdvanced({...advanced,filters:[...advanced.filters,{id:crypto.randomUUID(),field:'statusType',operator:'is',value:''}]})}>{t('Add filter')}</button><button type="button" onClick={()=>{onChange({...state,match:advanced.match,filters:advanced.filters.filter(isSearchConditionValid)});setAdvancedOpen(false)}}>{t('Apply filters')}</button></footer>
    </DialogContent></Dialog>
  </>
}

function searchDatePresets(now = new Date()) {
  const presets: Array<[string,Date]> = [
    ['1 day ago', subDays(now, 1)], ['3 days ago', subDays(now, 3)], ['1 week ago', subDays(now, 7)],
    ['1 month ago', subMonths(now, 1)], ['3 months ago', subMonths(now, 3)], ['6 months ago', subMonths(now, 6)], ['1 year ago', subYears(now, 1)],
  ]
  return presets.map(([label,date])=>({label,value:`${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`}))
}

function SearchFilterValue({field,value,onChange,onCancel,compact=false,users}:{field:SearchFilterField;value:string;onChange:(value:string,operator?:SearchCondition['operator'],granularity?:DateMode)=>void;onCancel?:()=>void;compact?:boolean;users?:User[]}) {
  const {t,locale}=useI18n()
  const directory=usePeopleDirectory()
  const phone=usePhoneLayout()
  if(field.endsWith('At')&&!compact)return <SearchDateFilter label={t(searchFields.find(item=>item.id===field)!.label)} value={value} onApply={onChange} onCancel={()=>onCancel?.()}/>
  if(field.endsWith('At')) return <div className="workspace-search-date-filter">
    <DateTimeControl locale={locale} label={t(searchFields.find(item=>item.id===field)!.label)} portal={phone} value={value} onChange={next=>onChange(next,undefined,'day')}/>
  </div>
  if(field==='statusType') return <PropertyMenu label="Status type" ariaLabel={t('Status type')} searchPlaceholder={t('Filter status types…')} embedded={!compact} compact={compact} selectedId={value} value={searchStatuses.find(item=>item.id===value)?.label} options={searchStatuses.map(item=>({...item,icon:<StatusIcon state={{id:item.id,name:item.label,type:item.id,color:'var(--text-secondary)'}}/>}))} onChange={onChange}/>
  const label=field==='creatorId'?'Creator':'Assignee / Lead'
  return <PersonPicker label={label} ariaLabel={t(label)} embedded={!compact} selectedId={value==='none'?'':value||'__unselected__'} value={(users??[...directory.users.values()]).find(user=>user.id===value)?.displayName} emptyOptionLabel="Unassigned" emptyTriggerLabel={label} searchPlaceholder={t('Search members…')} triggerClassName="mini-property-trigger" people={(users??[...directory.users.values()]).filter(user=>user.active).map(user=>({...user,label:user.displayName}))} onChange={id=>onChange(id||'none')}/>
}

export function SearchFilterChips({state,onChange,users,teams=[]}:{state:SearchPageState;onChange:(state:SearchPageState)=>void;users?:User[];teams?:Team[]}) {
  const {t,locale}=useI18n()
  const directory=usePeopleDirectory()
  const personName=(id:string)=>users?.find(user=>user.id===id)?.displayName??directory.users.get(id)?.displayName
  return <div className="workspace-search-filter-chips" aria-label={t('Active filters')}>{state.filters.map(filter=>filter.field==='team'?<FilterConditionChip key={filter.id} fieldVisual={<TeamIcon size={14}/>} filter={{...filter,fieldLabel:'Team',values:searchConditionValues(filter).map(id=>teamOption(teams.find(team=>team.id===id)??{id,name:id,key:''})),operatorChoices:['is','isNot','in','notIn'].map(operator=>({operator,label:searchOperatorLabel(operator)})),valueSummary:searchConditionValues(filter).length>1?t('{count} teams').replace('{count}',()=>String(searchConditionValues(filter).length)):undefined}} options={teams.map(teamOption)} translate={t} onOperatorChange={operator=>onChange({...state,filters:state.filters.map(item=>item.id===filter.id?{...item,operator:operator as SearchCondition['operator']}:item)})} onValuesChange={values=>onChange({...state,filters:values.length?state.filters.map(item=>item.id===filter.id?teamSelection(item,values.map(value=>value.id)):item):state.filters.filter(item=>item.id!==filter.id)})} onRemove={()=>onChange({...state,filters:state.filters.filter(item=>item.id!==filter.id)})}/>:<span key={filter.id}><span>{t(searchFields.find(field=>field.id===filter.field)!.label)} {t(searchOperatorLabel(filter.operator))} </span><strong data-i18n-ignore>{filter.field.endsWith('At')&&filter.granularity?searchDateLabel(filter.value,filter.granularity,locale):personName(filter.value)??(filter.value==='none'?t('Unassigned'):filter.field==='statusType'?t(searchStatuses.find(status=>status.id===filter.value)?.label??filter.value):filter.value)}</strong><button aria-label={t('Remove filter')} type="button" onClick={()=>onChange({...state,filters:state.filters.filter(item=>item.id!==filter.id)})}><X size={12}/></button></span>)}</div>
}

function searchOperatorLabel(operator: string) { return ({isNot:'is not',in:'is any of',notIn:'is none of'} as Record<string,string>)[operator]??operator }

function teamOption(team: Pick<Team,'id'|'name'|'key'> & Partial<Pick<Team,'icon'|'color'>>) {
  return {id:team.id,label:team.name,keywords:`${team.name} ${team.key}`,end:team.key,icon:<TeamIcon team={{...team,color:team.color??'var(--theme-text-secondary)'}} size={16}/>,i18nIgnore:true}
}

function SearchTeamValue({teams,values,onChange,onClose,compact=false}:{teams:Team[];values:string[];onChange:(values:string[])=>void;onClose?:()=>void;compact?:boolean}) {
  const {t}=useI18n()
  const directory=usePeopleDirectory()
  const options=teams.filter(team=>!team.archivedAt).map(team=>{
    const ancestors:Team[]=[]
    const seen=new Set([team.id])
    let parentId=directory.teamDirectory.get(team.id)?.parentId
    while(parentId&&!seen.has(parentId)){
      seen.add(parentId)
      const parent=teams.find(item=>item.id===parentId)
      if(!parent)break
      ancestors.unshift(parent)
      parentId=directory.teamDirectory.get(parent.id)?.parentId
    }
    return {...teamOption(team),keywords:[...ancestors.map(item=>item.name),team.name,team.key].join(' '),...(ancestors.length?{labelContent:<>{ancestors.map(parent=><span className="property-command-ancestor" key={parent.id}>{parent.name}<ChevronRight size={12}/></span>)}{team.name}</>}:{}),groupId:directory.teamIds.get(directory.viewerId??'')?.includes(team.id)?'mine':'other',groupLabel:t(directory.teamIds.get(directory.viewerId??'')?.includes(team.id)?'My teams':'Other teams')}
  })
  return <PropertyMenu label="Team" ariaLabel={t('Team')} embedded={!compact} compact={compact} multiple selectedIds={values} value={values.map(id=>teams.find(team=>team.id===id)?.name??id).join(', ')} valueIsEntityName searchPlaceholder={t('Search teams…')} options={options} onOpenChange={open=>{if(!open)onClose?.()}} onChange={id=>onChange(values.includes(id)?values.filter(value=>value!==id):[...values,id])}/>
}
