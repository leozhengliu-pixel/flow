/** The identifier a name suggests: its first three letters or digits, with a number appended while another team uses it. */
export function suggestTeamKey(name: string, taken: Iterable<string>) {
  const used = new Set([...taken].map((key) => key.toUpperCase()));
  const base = name.replace(/[^a-zA-Z0-9]/g, "").slice(0, 3).toUpperCase();
  if (!base) return "";
  let key = base;
  for (let suffix = 2; used.has(key); suffix += 1) key = `${base}${suffix}`;
  return key;
}
