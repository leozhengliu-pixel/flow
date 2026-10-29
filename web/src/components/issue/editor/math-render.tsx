import katex from 'katex'
import { useMemo } from 'react'
import 'katex/dist/katex.min.css'
import './math-block.css'

export function MathRender({ source }: { source: string }) {
  const html = useMemo(() => katex.renderToString(source, {
    displayMode: true,
    throwOnError: false,
    trust: false,
    strict: 'ignore',
    maxExpand: 1000,
    maxSize: 20,
  }), [source])
  return <div className="math-block-render" dangerouslySetInnerHTML={{ __html: html }}/>
}
