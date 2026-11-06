import { Fragment, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import type { PersonalAgentSkill } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { AppLink } from "@/components/ui/app-link";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { AgentSkillsIcon } from "./agent-icons";
import { skillSummary } from "./agent-skill-menu";
import styles from "./agent-slash-menu.module.css";

/** One row of the "/" menu: a skill (listed under "Skills") or a command with an icon and an optional key hint. */
export type SlashEntry =
  | { kind: "skill"; key: string; skill: PersonalAgentSkill }
  | { kind: "command"; key: string; label: string; icon: ReactNode; shortcut?: string; group: number; run: () => void };

/** Where the menu opens: `top` is 4px under the line holding the "/", `lineTop` that line's top (the menu flips above when it must). */
export type SlashAnchor = { left: number; top: number; lineTop: number };

/**
 * The agent composer's "/" menu, as in the reference app: a "Skills" section (no icons, the newest four until something is
 * typed), then the commands the composer supports, in groups with hairlines between them. The focused skill shows a hover
 * card beside the menu: its name (a link to the skill), what it does, and whose it is.
 */
export function AgentSlashMenu({ entries, activeIndex, anchor, skillHref, onActive, onChoose, onDismiss }: {
  entries: SlashEntry[];
  activeIndex: number;
  anchor: SlashAnchor;
  skillHref: (skill: PersonalAgentSkill) => string;
  onActive: (index: number) => void;
  onChoose: (entry: SlashEntry) => void;
  onDismiss: () => void;
}) {
  const { t } = useI18n();
  const menuRef = useRef<HTMLDivElement>(null);
  const rowRef = useRef<HTMLButtonElement>(null);
  const [placement, setPlacement] = useState<{ top: number; maxHeight?: number }>({ top: anchor.top });
  const [card, setCard] = useState<{ left: number; top: number }>();
  const active = entries[activeIndex];
  const activeSkill = active?.kind === "skill" ? active.skill : undefined;

  // Opens under the line, or above it when it would run off the window.
  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!menu) return;
    const height = menu.offsetHeight;
    const room = window.innerHeight - anchor.top - 8;
    const above = anchor.lineTop - 4 - height;
    const next = height <= room ? { top: anchor.top } : above >= 8 ? { top: above } : { top: anchor.top, maxHeight: Math.max(room, 160) };
    setPlacement((current) => (current.top === next.top && current.maxHeight === next.maxHeight ? current : next));
  }, [anchor.lineTop, anchor.top, entries.length]);

  // The focused row stays in view; the hover card sits beside the menu, level with that row.
  useLayoutEffect(() => {
    rowRef.current?.scrollIntoView?.({ block: "nearest" });
    const menu = menuRef.current;
    const row = rowRef.current;
    if (!activeSkill || !menu || !row) { setCard(undefined); return; }
    const box = menu.getBoundingClientRect();
    const line = row.getBoundingClientRect();
    setCard({ left: box.right + 4.5, top: line.top });
  }, [activeIndex, activeSkill, placement.top, entries.length]);

  let skillsHeaded = false;
  return createPortal(<>
    <div className={styles.menu} data-agent-slash="" ref={menuRef} role="listbox" aria-label={t("Insert block")} style={{ left: anchor.left, top: placement.top, maxHeight: placement.maxHeight }} onMouseDown={(event) => event.preventDefault()}>
      <div className={styles.list}>
        {entries.length === 0 ? <div className={styles.empty} data-focused="" role="status">
          <span>{t("No results found")}</span>
          <button type="button" onClick={onDismiss}>{t("Dismiss")}</button>
        </div> : entries.map((entry, index) => {
          const previous = entries[index - 1];
          const heading = entry.kind === "skill" && !skillsHeaded;
          if (entry.kind === "skill") skillsHeaded = true;
          const divider = entry.kind === "command" && previous && (previous.kind === "skill" || previous.group !== entry.group);
          return <Fragment key={entry.key}>
            {heading && <div className={styles.heading}><span>{t("Skills")}</span></div>}
            {divider && <div className={styles.separator} role="separator" />}
            <button ref={index === activeIndex ? rowRef : undefined} className={styles.row} data-focused={index === activeIndex ? "" : undefined} role="option" aria-selected={index === activeIndex} type="button" onClick={() => onChoose(entry)} onMouseMove={() => { if (index !== activeIndex) onActive(index); }}>
              <span className={styles.rowBody}>
                {entry.kind === "command" && <span className={styles.icon}>{entry.icon}</span>}
                <span className={styles.label} data-i18n-ignore={entry.kind === "skill" ? "" : undefined}>{entry.kind === "skill" ? entry.skill.name : t(entry.label)}</span>
                {entry.kind === "command" && entry.shortcut && <span className={styles.keys} aria-hidden="true">{[...entry.shortcut.split(" ")].map((key, keyIndex) => <kbd key={keyIndex}>{key}</kbd>)}</span>}
              </span>
            </button>
          </Fragment>;
        })}
      </div>
    </div>
    {activeSkill && card && <div className={styles.card} data-agent-slash-card="" style={{ left: card.left, top: card.top }}>
      <div className={styles.cardBody}>
        <div className={styles.cardTitle}>
          <span className={styles.cardIcon}><AgentSkillsIcon size={16} /></span>
          <AppLink className={styles.cardLink} href={skillHref(activeSkill)} onMouseDown={(event) => event.preventDefault()}><span data-i18n-ignore>{activeSkill.name}</span><FlowGlyph name="externalArrow" size={12} /></AppLink>
        </div>
        {skillSummary(activeSkill) && <span className={styles.cardText} data-i18n-ignore>{skillSummary(activeSkill)}</span>}
        <div className={styles.cardOwner}><FlowGlyph name="assignee" size={16} /><span>{t("Personal")}</span></div>
      </div>
    </div>}
  </>, document.body);
}
