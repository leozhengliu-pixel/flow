import { afterEach, expect, it } from 'vitest'
import { Editor } from '@tiptap/core'
import { FlowStarterKit } from '@/components/editor/flow-starter-kit'
import { ProjectDescriptionText } from './project-description-text'

let editor: Editor | undefined
afterEach(() => editor?.destroy())

it('wraps visible project text without changing stored content, copied HTML or editing', () => {
  editor = new Editor({
    element: document.createElement('div'),
    extensions: [FlowStarterKit.configure({ undoRedo: false }), ProjectDescriptionText],
    content: '<p>Typography</p><h2>Goals</h2><ul><li><p>Grouping</p></li></ul>',
  })
  const original = editor.getJSON()
  expect([...editor.view.dom.querySelectorAll('.project-description-text')].map(node => node.textContent)).toEqual(['Typography', 'Goals', 'Grouping'])
  expect(editor.getHTML()).not.toContain('project-description-text')
  expect(editor.getJSON()).toEqual(original)
  editor.commands.insertContentAt(1, 'More ')
  expect(editor.view.dom.querySelector('.project-description-text')?.textContent).toBe('More Typography')
  expect(editor.getText()).toContain('More Typography')
})
