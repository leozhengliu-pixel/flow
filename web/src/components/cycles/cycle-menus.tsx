import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { CalendarDays, Check, ChevronRight, Edit3, MoreHorizontal, Play, SlidersHorizontal } from 'lucide-react'
import { useEffect, useState } from 'react'
import type { Cycle, CycleMutationInput } from '@/types/flow'

export function CycleActions({ cycle, onUpdate, onStart, onComplete }: {
  cycle: Cycle
  onUpdate: (input: CycleMutationInput) => Promise<unknown>
  onStart: () => Promise<unknown>
  onComplete: () => Promise<unknown>
}) {
  const [editor, setEditor] = useState<'details'|'dates'>()
  const [confirm, setConfirm] = useState<'start'|'complete'>()
  return <>
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild><button aria-label={`${cycle.name} actions`} className="cycle-icon-button" type="button"><MoreHorizontal size={15}/></button></DropdownMenu.Trigger>
      <DropdownMenu.Portal><DropdownMenu.Content align="end" className="cycle-menu" collisionPadding={10} sideOffset={5}>
        <DropdownMenu.Item onSelect={() => setEditor('details')}><Edit3 size={14}/><span>Edit cycle</span><kbd>E</kbd></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => setEditor('dates')}><CalendarDays size={14}/><span>Change dates…</span></DropdownMenu.Item>
        <DropdownMenu.Sub><DropdownMenu.SubTrigger><SlidersHorizontal size={14}/><span>Cycle actions</span><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="cycle-menu" sideOffset={4} alignOffset={-5}>
          {cycle.status === 'upcoming' && <DropdownMenu.Item onSelect={() => setConfirm('start')}><Play size={14}/><span>Start cycle today</span></DropdownMenu.Item>}
          {cycle.status === 'current' && <DropdownMenu.Item onSelect={() => setConfirm('complete')}><Check size={14}/><span>Complete cycle</span></DropdownMenu.Item>}
          <DropdownMenu.Item onSelect={() => setEditor('dates')}><CalendarDays size={14}/><span>Move or resize cycle</span></DropdownMenu.Item>
        </DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      </DropdownMenu.Content></DropdownMenu.Portal>
    </DropdownMenu.Root>
    <CycleEditor cycle={cycle} mode={editor} open={Boolean(editor)} onOpenChange={open => { if (!open) setEditor(undefined) }} onSubmit={onUpdate}/>
    <CycleConfirm cycle={cycle} kind={confirm} open={Boolean(confirm)} onOpenChange={open => { if (!open) setConfirm(undefined) }} onConfirm={async () => { if (confirm === 'start') await onStart(); else await onComplete(); setConfirm(undefined) }}/>
  </>
}

function CycleEditor({ cycle, mode, open, onOpenChange, onSubmit }: { cycle: Cycle; mode?: 'details'|'dates'; open: boolean; onOpenChange: (open: boolean) => void; onSubmit: (input: CycleMutationInput) => Promise<unknown> }) {
  const [name, setName] = useState(cycle.name)
  const [description, setDescription] = useState(cycle.description)
  const [startsAt, setStartsAt] = useState(cycle.startsAt.slice(0, 10))
  const [endsAt, setEndsAt] = useState(cycle.endsAt.slice(0, 10))
  const [capacity, setCapacity] = useState(cycle.capacity)
  const [saving, setSaving] = useState(false)
  useEffect(() => { setName(cycle.name); setDescription(cycle.description); setStartsAt(cycle.startsAt.slice(0, 10)); setEndsAt(cycle.endsAt.slice(0, 10)); setCapacity(cycle.capacity) }, [cycle, open])
  const submit = async () => {
    if (!name.trim() || !startsAt || !endsAt || startsAt >= endsAt) return
    setSaving(true)
    try { await onSubmit({ name: name.trim(), description: description.trim(), startsAt, endsAt, capacity }); onOpenChange(false) } finally { setSaving(false) }
  }
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="cycle-dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="cycle-dialog">
    <Dialog.Title>{mode === 'dates' ? 'Change cycle dates' : 'Edit cycle'}</Dialog.Title>
    <div className="cycle-dialog__fields">
      {mode !== 'dates' && <><label><span>Name</span><input autoFocus value={name} onChange={event => setName(event.target.value)}/></label><label><span>Description</span><textarea placeholder="Add a description…" value={description} onChange={event => setDescription(event.target.value)}/></label></>}
      <div><label><span>Start date</span><input type="date" value={startsAt} onChange={event => setStartsAt(event.target.value)}/></label><label><span>End date</span><input type="date" value={endsAt} onChange={event => setEndsAt(event.target.value)}/></label></div>
      {mode !== 'dates' && <label><span>Capacity</span><input min="0" type="number" value={capacity} onChange={event => setCapacity(Number(event.target.value))}/></label>}
    </div>
    <footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={saving || !name.trim() || startsAt >= endsAt} onClick={() => void submit()} type="button">{saving ? 'Saving…' : 'Save changes'}</button></footer>
  </Dialog.Content></Dialog.Portal></Dialog.Root>
}

function CycleConfirm({ cycle, kind, open, onOpenChange, onConfirm }: { cycle: Cycle; kind?: 'start'|'complete'; open: boolean; onOpenChange: (open: boolean) => void; onConfirm: () => Promise<void> }) {
  const [saving, setSaving] = useState(false)
  const action = kind === 'start' ? 'Start cycle today' : 'Complete cycle'
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="cycle-dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="cycle-dialog cycle-dialog--confirm">
    <Dialog.Title>{action}?</Dialog.Title><p>{kind === 'start' ? `The current cycle will complete and unfinished issues will move to ${cycle.name}.` : 'Unfinished issues will roll over to the next cycle.'}</p>
    <footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={saving} onClick={() => { setSaving(true); void onConfirm().finally(() => setSaving(false)) }} type="button">{saving ? 'Updating…' : action}</button></footer>
  </Dialog.Content></Dialog.Portal></Dialog.Root>
}

