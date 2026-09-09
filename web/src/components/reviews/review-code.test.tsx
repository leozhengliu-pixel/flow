import {render,screen} from '@testing-library/react'
import {afterEach,expect,it} from 'vitest'
import {setRuntimePreferences} from '@/lib/runtime-preferences'
import {ReviewCode} from './review-code'
afterEach(()=>setRuntimePreferences({}))
it('applies the selected code presentation and escapes source markup',()=>{
  setRuntimePreferences({codeTheme:'GitHub Dark',codeFont:'14px, Regular, Default'})
  const {container}=render(<ReviewCode path="example.ts" content={'const text = "<img src=x onerror=alert(1)>"'}/>)
  const code=container.querySelector('code')!
  expect(code).toHaveAttribute('data-code-theme','github-dark')
  expect(code.style.getPropertyValue('--review-code-size')).toBe('14px')
  expect(container.querySelector('.hljs-keyword')).toHaveTextContent('const')
  expect(container.querySelector('img')).toBeNull()
  expect(screen.getByText('"<img src=x onerror=alert(1)>"')).toBeInTheDocument()
})
