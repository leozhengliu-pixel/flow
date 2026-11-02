const graphemes = new Intl.Segmenter(undefined, { granularity: 'grapheme' });
const wideGlyph = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}\uFF01-\uFF60]/u;

function letters(value: string) {
  return Array.from(graphemes.segment(value), item => item.segment);
}

export function workspaceInitials(name: string) {
  const words = name.split(/\s+/)
    .map(word => letters(word).filter(letter => /[\p{L}\p{N}]/u.test(letter)))
    .filter(word => word.length > 0);
  const value = words.length > 1
    ? words.slice(0, 2).map(word => word[0]).join('')
    : (words[0] ?? []).slice(0, 2).join('');
  return letters(value.toUpperCase()).slice(0, 2).join('') || 'W';
}

export function hasWideWorkspaceInitials(value: string) {
  return letters(value).length > 1 && wideGlyph.test(value);
}
