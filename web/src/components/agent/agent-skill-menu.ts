import type { PersonalAgentSkill } from "@/types/flow";
import { fuzzyContains, normalizeSearchText } from "@/lib/search-match";

/** The "/" menu lists this many skills while nothing is typed after the slash (a query searches them all). */
export const SLASH_SKILL_LIMIT = 4;
/** The "+" menu's skill list gets a visible search field once it holds more skills than this. */
export const SKILL_SEARCH_AFTER = 3;

/** The skills as the composer menus list them: the newest first. */
export function orderedAgentSkills(skills: readonly PersonalAgentSkill[] | undefined): PersonalAgentSkill[] {
  return [...(skills ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

/** Whether a skill answers a typed query: its name contains the characters in order. */
export function skillMatches(skill: Pick<PersonalAgentSkill, "name">, query: string) {
  const needle = normalizeSearchText(query);
  return !needle || fuzzyContains(normalizeSearchText(skill.name), needle);
}

/** The line a skill's hover card shows under its name: the first line of its instructions. */
export function skillSummary(skill: Pick<PersonalAgentSkill, "instructions">) {
  return skill.instructions.split("\n").map((line) => line.trim()).find(Boolean) ?? "";
}

/** How a skill reads in the text of a message: "/Name". */
export function skillToken(skill: Pick<PersonalAgentSkill, "name">) {
  return `/${skill.name}`;
}

const boundary = (character: string | undefined) => character === undefined || /\s/.test(character);

/** A text split at the skills it names: plain text and skills, in order (a draft's "/Name" turns back into a chip). */
export function splitSkillTokens(text: string, skills: readonly PersonalAgentSkill[] | undefined): (string | PersonalAgentSkill)[] {
  const parts: (string | PersonalAgentSkill)[] = [];
  const known = [...(skills ?? [])].sort((a, b) => b.name.length - a.name.length);
  let plain = "";
  for (let index = 0; index < text.length;) {
    const skill = text[index] === "/" && boundary(text[index - 1])
      ? known.find((item) => text.startsWith(skillToken(item), index) && boundary(text[index + skillToken(item).length]))
      : undefined;
    if (skill) {
      if (plain) parts.push(plain);
      plain = "";
      parts.push(skill);
      index += skillToken(skill).length;
    } else {
      plain += text[index];
      index += 1;
    }
  }
  if (plain) parts.push(plain);
  return parts;
}

/** The skills a message text names with "/Name" (a composer without chips keeps the token as plain text). */
export function skillIdsInText(text: string, skills: readonly PersonalAgentSkill[] | undefined) {
  return [...new Set(splitSkillTokens(text, skills).flatMap((part) => (typeof part === "string" ? [] : [part.id])))];
}
