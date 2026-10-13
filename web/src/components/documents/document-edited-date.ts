/** "Sep 29" for this year, "Sep 29, 2025" for another: the reference never uses relative times here, even for today. */
export function documentEditedDate(value: string, formatDate: (value: Date | string | number, options?: Intl.DateTimeFormatOptions) => string, now = new Date()) {
  const sameYear = new Date(value).getFullYear() === now.getFullYear()
  return formatDate(value, sameYear ? { month: 'short', day: 'numeric' } : { year: 'numeric', month: 'short', day: 'numeric' })
}
