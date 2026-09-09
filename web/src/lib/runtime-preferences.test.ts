import { afterEach,expect,it } from 'vitest'
import { commentShortcutMatches, displayUserName, firstWeekday, setRuntimePreferences, transformTextEmoticons } from './runtime-preferences'
afterEach(()=>setRuntimePreferences({}))
it('applies comment shortcuts while preserving newline and IME input',()=>{
  const enter={key:'Enter',shiftKey:false,ctrlKey:false,metaKey:false,altKey:false}
  setRuntimePreferences({sendComments:'Enter'})
  expect(commentShortcutMatches(enter)).toBe(true)
  expect(commentShortcutMatches({...enter,shiftKey:true})).toBe(false)
  expect(commentShortcutMatches({...enter,nativeEvent:{isComposing:true}})).toBe(false)
  setRuntimePreferences({sendComments:'⌘ Enter'})
  expect(commentShortcutMatches(enter)).toBe(false)
  expect(commentShortcutMatches({...enter,metaKey:true})).toBe(true)
})
it('formats names, first weekdays and text emoticons according to preferences',()=>{
  setRuntimePreferences({displayNames:'Username',firstDay:'Sunday',emoticons:true})
  expect(displayUserName({displayName:'Alex Lee',name:'alex.lee'})).toBe('alex.lee')
  expect(firstWeekday()).toBe(0)
  expect(transformTextEmoticons('Hello :)')).toBe('Hello 🙂')
  setRuntimePreferences({displayNames:'First name',emoticons:false})
  expect(displayUserName({displayName:'Alex Lee',name:'alex.lee'})).toBe('Alex')
  expect(transformTextEmoticons('Hello :)')).toBe('Hello :)')
})
