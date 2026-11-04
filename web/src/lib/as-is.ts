/**
 * The replacement for `text.replace('{name}', asIs(value))`. A plain string replacement expands `$&`, `$'` and `$$` inside the
 * value (a person called "A$&B" would read "A{name}B"); a replacer function inserts it as written. The value is read here, not
 * inside a callback, so TypeScript keeps the narrowing of the surrounding expression.
 */
export function asIs(value: string | number): () => string {
  const text = String(value)
  return () => text
}
