import {expect,it} from 'vitest'
import {makeBootstrap} from '@/test/fixtures'
import {searchResultLink} from './search-result-link'

it('navigates uncached issue results and server-provided entity slugs',()=>{
 const data=makeBootstrap({issues:[],projects:[],documents:[],savedViews:[]})
 expect(searchResultLink(data,{id:'unloaded',type:'issue',identifier:'TST-80000',title:'Found issue',score:1})).toBe('/workspace/issue/TST-80000/found-issue')
 expect(searchResultLink(data,{id:'unloaded',type:'project',slugId:'found-project',title:'Found project',score:1})).toBe('/workspace/project/found-project/overview')
 expect(searchResultLink(data,{id:'unloaded',type:'document',url:'/workspace/document/a-document',title:'Doc',score:1})).toBe('/workspace/document/a-document')
})
it('rejects external or another workspace URL returned by a search result',()=>{
 const data=makeBootstrap({issues:[]}),result={id:'id',type:'issue' as const,identifier:'TST-1',title:'Safe',score:1}
 for(const url of ['https://example.test/','//example.test/','/other/issue/TST-1','/workspace/\\example.test','/workspace/../other/issue/TST-1','/workspace/%2e%2e/other/issue/TST-1','/workspace/.%2e/other/issue/TST-1'])expect(searchResultLink(data,{...result,url})).toBe('/workspace/issue/TST-1/safe')
})
