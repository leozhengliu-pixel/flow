import { useEffect, useState } from 'react'

export function useIssueSurfaceControls(filterOpenSignal: number, detailsOpen: boolean, onDetailsOpenChange?: (open: boolean) => void) {
  const [filterOpen,setFilterOpen]=useState(false),[displayOpen,setDisplayOpen]=useState(false)
  const changeFilterOpen=(open:boolean)=>{setFilterOpen(open);if(open)setDisplayOpen(false)}
  const changeDisplayOpen=(open:boolean)=>{setDisplayOpen(open);if(open)setFilterOpen(false)}
  useEffect(()=>{if(filterOpenSignal>0){setDisplayOpen(false);setFilterOpen(true)}},[filterOpenSignal])
  useEffect(()=>{const toggle=(event:KeyboardEvent)=>{if(!(event.metaKey||event.ctrlKey)||event.key.toLowerCase()!=='i'||(event.target as HTMLElement|null)?.closest('input,textarea,[contenteditable="true"],[role="textbox"]'))return;event.preventDefault();onDetailsOpenChange?.(!detailsOpen)};addEventListener('keydown',toggle);return()=>removeEventListener('keydown',toggle)},[detailsOpen,onDetailsOpenChange])
  return {changeDisplayOpen,changeFilterOpen,displayOpen,filterOpen}
}
