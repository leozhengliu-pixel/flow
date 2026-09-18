const defaultSource = 'flowchart TD\n    A[Start] --> B[End]'

export async function renderMermaidPreview(target: HTMLElement, source: string) {
  const mermaid = (await import('mermaid')).default
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: document.documentElement.dataset.theme === 'light' ? 'neutral' : 'dark',
  })
  const id = `diagram-${Math.random().toString(36).slice(2)}`
  const { svg } = await mermaid.render(id, source || defaultSource)
  target.innerHTML = svg
}
