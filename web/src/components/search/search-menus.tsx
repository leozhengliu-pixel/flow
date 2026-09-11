import { useState } from 'react'
import { Calendar, Check, Filter, SlidersHorizontal, UserRound, X } from 'lucide-react'
import { PropertyMenu } from '@/components/property/property-menu'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import { StatusIcon } from '@/components/issue/issue-icons'
import { usePeopleDirectory } from '@/components/property/people-context'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { SelectControl } from '@/components/ui/select-control'
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuLabel, DropdownMenuSub, DropdownMenuSubTrigger, DropdownMenuSubContent, DropdownMenuCheckboxItem } from '@/components/ui/dropdown-menu'
import { DisplayIcon, FilterIcon } from '@/components/ui/view-action-icons'
import { useI18n } from '@/i18n/i18n'
import { searchFields, type SearchPageState, type SearchFilterField, type SearchCondition, type SearchOrder } from './search-state'

export const searchStatuses = [{id:'backlog',label:'Backlog'},{id:'unstarted',label:'Unstarted'},{id:'started',label:'Started'},{id:'completed',label:'Completed'},{id:'canceled',label:'Canceled'}]
const orderOptions: Array<{id:SearchOrder;label:string}> = [{id:'relevance',label:'Most relevant'},{id:'updatedAt',label:'Last updated'},{id:'createdAt',label:'Last created'},{id:'title',label:'Title'}]

export function SearchMenus({state,onChange}:{state:SearchPageState;onChange:(state:SearchPageState)=>void}) {
  const {t}=useI18n()
  const [filterOpen,setFilterOpen]=useState(false)
  const [advancedOpen,setAdvancedOpen]=useState(false)
  const [advanced,setAdvanced]=useState(state)
  const add=(field:SearchFilterField,value:string,operator:SearchCondition['operator']='is')=>{
    onChange({...state,filters:[...state.filters,{id:crypto.randomUUID(),field,value,operator}]})
    setFilterOpen(false)
  }
  return <>
    <DropdownMenu open={filterOpen} onOpenChange={setFilterOpen}>
      <DropdownMenuTrigger asChild><button className="workspace-search-tool" type="button" aria-label={t('Add filter')} title={t('Add filter')}><FilterIcon/></button></DropdownMenuTrigger>
      <DropdownMenuContent className="workspace-search-menu" align="end" aria-label={t('Search filters')}>
        <DropdownMenuItem onSelect={()=>{setAdvanced(state);setAdvancedOpen(true)}}><SlidersHorizontal size={15}/>{t('Advanced filter')}</DropdownMenuItem>
        <DropdownMenuSeparator/>
        {searchFields.map(field=><DropdownMenuSub key={field.id}>
          <DropdownMenuSubTrigger>{field.id.endsWith('At')?<Calendar size={15}/>:field.id==='statusType'?<Filter size={15}/>:<UserRound size={15}/>}<span>{t(field.label)}</span></DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="workspace-search-value-menu" onKeyDown={event=>event.stopPropagation()}>
            <SearchFilterValue field={field.id} value="" onChange={(value,operator)=>add(field.id,value,operator)}/>
          </DropdownMenuSubContent>
        </DropdownMenuSub>)}
      </DropdownMenuContent>
    </DropdownMenu>
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
    <Dialog open={advancedOpen} onOpenChange={setAdvancedOpen}><DialogContent className="workspace-search-advanced" aria-describedby={undefined}><DialogTitle>{t('Advanced filter')}</DialogTitle>
      <SelectControl label={t('Match filters')} value={advanced.match} options={[{value:'and',label:t('All filters')},{value:'or',label:t('Any filter')}]} onChange={match=>setAdvanced({...advanced,match:match as 'and'|'or'})}/>
      <div className="workspace-search-conditions">{advanced.filters.map(condition=><div className="workspace-search-condition" key={condition.id}>
        <SelectControl label={t('Filter field')} value={condition.field} options={searchFields.map(field=>({value:field.id,label:t(field.label)}))} onChange={field=>setAdvanced({...advanced,filters:advanced.filters.map(item=>item.id===condition.id?{...item,field:field as SearchFilterField,value:'',operator:field.endsWith('At')?'after':'is'}:item)})}/>
        <SelectControl label={t('Filter operator')} value={condition.operator} options={(condition.field.endsWith('At')?['after','before']:['is','isNot']).map(operator=>({value:operator,label:t(operator==='isNot'?'is not':operator)}))} onChange={operator=>setAdvanced({...advanced,filters:advanced.filters.map(item=>item.id===condition.id?{...item,operator:operator as SearchCondition['operator']}:item)})}/>
        <SearchFilterValue field={condition.field} value={condition.value} compact onChange={value=>setAdvanced({...advanced,filters:advanced.filters.map(item=>item.id===condition.id?{...item,value}:item)})}/>
        <button type="button" aria-label={t('Remove filter')} onClick={()=>setAdvanced({...advanced,filters:advanced.filters.filter(item=>item.id!==condition.id)})}><X size={14}/></button>
      </div>)}</div>
      <footer><button type="button" onClick={()=>setAdvanced({...advanced,filters:[...advanced.filters,{id:crypto.randomUUID(),field:'statusType',operator:'is',value:''}]})}>{t('Add filter')}</button><button type="button" onClick={()=>{onChange({...state,match:advanced.match,filters:advanced.filters.filter(item=>item.value)});setAdvancedOpen(false)}}>{t('Apply filters')}</button></footer>
    </DialogContent></Dialog>
  </>
}

function SearchFilterValue({field,value,onChange,compact=false}:{field:SearchFilterField;value:string;onChange:(value:string,operator?:SearchCondition['operator'])=>void;compact?:boolean}) {
  const {t}=useI18n()
  const directory=usePeopleDirectory()
  const [operator,setOperator]=useState<'after'|'before'>('after')
  if(field.endsWith('At')) return <div className="workspace-search-date-filter">
    {!compact&&<SelectControl label={t('Filter operator')} value={operator} options={[{value:'after',label:t('After')},{value:'before',label:t('Before')}]} onChange={next=>setOperator(next as 'after'|'before')}/>}
    <DateTimeControl label={t(searchFields.find(item=>item.id===field)!.label)} value={value} onChange={next=>onChange(next,operator)}/>
  </div>
  if(field==='statusType') return <PropertyMenu label="Status type" embedded={!compact} compact={compact} selectedId={value} value={searchStatuses.find(item=>item.id===value)?.label} options={searchStatuses.map(item=>({...item,icon:<StatusIcon state={{id:item.id,name:item.label,type:item.id,color:'var(--text-secondary)'}}/>}))} onChange={onChange}/>
  const label=field==='creatorId'?'Creator':'Assignee / Lead'
  return <PersonPicker label={label} ariaLabel={t(label)} embedded={!compact} selectedId={value==='none'?'':value||'__unselected__'} value={directory.users.get(value)?.displayName} emptyOptionLabel="Unassigned" emptyTriggerLabel={label} searchPlaceholder="Search members…" triggerClassName="mini-property-trigger" people={[...directory.users.values()].filter(user=>user.active).map(user=>({...user,label:user.displayName}))} onChange={id=>onChange(id||'none')}/>
}

export function SearchFilterChips({state,onChange}:{state:SearchPageState;onChange:(state:SearchPageState)=>void}) {
  const {t}=useI18n()
  const directory=usePeopleDirectory()
  return <div className="workspace-search-filter-chips" aria-label={t('Active filters')}>{state.filters.map(filter=><span key={filter.id}><span>{t(searchFields.find(field=>field.id===filter.field)!.label)} {t(filter.operator==='isNot'?'is not':filter.operator)} </span><strong data-i18n-ignore>{directory.users.get(filter.value)?.displayName??(filter.field==='statusType'?t(searchStatuses.find(status=>status.id===filter.value)?.label??filter.value):filter.value)}</strong><button aria-label={t('Remove filter')} type="button" onClick={()=>onChange({...state,filters:state.filters.filter(item=>item.id!==filter.id)})}><X size={12}/></button></span>)}</div>
}
