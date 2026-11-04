/**
 * The Skills card of Agent personalization: a header with the skill count and a create button, then one row per
 * skill with its name, when it was created or last updated and a row menu. Instructions are Markdown and are
 * read and edited on the skill's own page, so the list shows no preview of them.
 */
import { NavLink } from "react-router-dom";
import { Plus } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import { agentSkillPath, newAgentSkillPath } from "@/lib/app-routes";
import type { BootstrapData, PersonalAgentSkill } from "@/types/flow";
import { AgentSkillActionsMenu, AgentSkillContextMenu } from "./agent-skill-actions";
import { useAgentSkillActions } from "./use-agent-skill-actions";
import "./agent-skills.css";

function skillTimestampLabel(skill: PersonalAgentSkill, t: (source: string) => string, relative: (value: string) => string) {
  // A skill that was never edited reads "Created", any later change reads "Updated".
  const edited = Date.parse(skill.updatedAt) - Date.parse(skill.createdAt) > 1000;
  return `${t(edited ? "Updated" : "Created")} ${relative(edited ? skill.updatedAt : skill.createdAt)}`;
}

export function AgentSkillsList({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const { t, locale, formatDate, formatRelative } = useI18n();
  const actions = useAgentSkillActions(data, onReload);
  const skills = data.agentSkills;
  const count = skills.length;
  const title = count
    ? locale === "zh-CN"
      ? `${count} 项技能`
      : `${count} ${count === 1 ? "skill" : "skills"}`
    : t("No skills created");
  return (
    <div className="agent-skills" data-i18n-ignore>
      <header className="agent-skills-header">
        <h3 className={count ? undefined : "is-empty"}>{title}</h3>
        <NavLink className="agent-skills-create" to={newAgentSkillPath(data.workspace.urlKey)} aria-label={t("Create skill")}>
          <Plus size={14} aria-hidden="true" />
        </NavLink>
      </header>
      {count > 0 && (
        <ul className="agent-skills-list">
          {skills.map((skill) => {
            const edited = Date.parse(skill.updatedAt) - Date.parse(skill.createdAt) > 1000;
            return (
              <AgentSkillContextMenu key={skill.id} skill={skill} actions={actions}>
                <li className="agent-skills-row">
                  <NavLink className="agent-skill-link" to={agentSkillPath(data.workspace.urlKey, skill.id)} aria-label={skill.name}>
                    <strong>{skill.name}</strong>
                    <small title={formatDate(edited ? skill.updatedAt : skill.createdAt, { dateStyle: "medium", timeStyle: "short" })}>
                      {skillTimestampLabel(skill, t, formatRelative)}
                    </small>
                  </NavLink>
                  <AgentSkillActionsMenu skill={skill} actions={actions} />
                </li>
              </AgentSkillContextMenu>
            );
          })}
        </ul>
      )}
    </div>
  );
}
