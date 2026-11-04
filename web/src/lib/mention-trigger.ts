/**
 * What may stand right before an "@" for it to start a mention: whitespace, an opening bracket, or CJK text and punctuation
 * (Chinese, Japanese and Korean are written without spaces, so "请@王伟" and "，@Anna Li" are mentions). A Latin letter or digit
 * before it ("a@b.com") makes the "@" part of an address instead.
 */
const MENTION_LEAD = String.raw`\s(\[{\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}　-〿！-／：-＠［-｀｛-･‘-‟—…`

/** `(^|lead)@` as regexp source, capturing the lead character (empty at the start). Needs the `u` flag. */
export const MENTION_LEAD_SOURCE = `(^|[${MENTION_LEAD}])`

/** The "@query" being typed at the end of the text before the caret; group 1 is the query. Needs no flags. */
export function mentionTriggerBeforeCaret(maxQueryLength?: number) {
  const length = maxQueryLength === undefined ? '*' : `{0,${maxQueryLength}}`
  return new RegExp(`(?:^|[${MENTION_LEAD}])@([^\\s@]${length})$`, 'u')
}
