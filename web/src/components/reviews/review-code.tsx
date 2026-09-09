import {useMemo,type CSSProperties} from 'react'
import hljs from 'highlight.js/lib/common'
import {useUserPreferences} from '@/lib/runtime-preferences'
import './review-code.css'

const languages:Record<string,string>={ts:'typescript',tsx:'typescript',js:'javascript',jsx:'javascript',mjs:'javascript',cjs:'javascript',go:'go',py:'python',rb:'ruby',rs:'rust',java:'java',kt:'kotlin',json:'json',css:'css',scss:'scss',html:'xml',xml:'xml',vue:'xml',yaml:'yaml',yml:'yaml',sh:'bash',bash:'bash',sql:'sql',md:'markdown',c:'c',h:'c',cpp:'cpp'}

export function ReviewCode({content,path,theme:themeOverride,font:fontOverride}:{content:string;path:string;theme?:string;font?:string}) {
  const preferences=useUserPreferences()
  const name=languages[path.split('.').at(-1)?.toLowerCase()??'']??'plaintext'
  const language=hljs.getLanguage(name)&&content.length<10000?name:'plaintext'
  const html=useMemo(()=>hljs.highlight(content,{language,ignoreIllegals:true}).value,[content,language])
  const font=Math.max(10,Math.min(24,Number.parseInt(fontOverride??preferences.codeFont??'12',10)||12))
  const theme=(themeOverride??preferences.codeTheme??'Flow Light').toLowerCase().replaceAll(' ','-')
  return <code data-i18n-ignore data-code-theme={theme} className="review-code" style={{'--review-code-size':`${font}px`} as CSSProperties} dangerouslySetInnerHTML={{__html:html}}/>
}
