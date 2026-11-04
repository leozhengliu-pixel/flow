/**
 * A personal agent skill's page (`/settings/skill/new` and `/settings/skill/:id`): a title field and the skill's
 * instructions in the shared rich-text editor, which stores them as Markdown. Cancel and Save sit under the card,
 * the title row carries the skill menu, and leaving with unsaved changes asks to discard them.
 */
import { useEffect, useRef, useState } from "react";
import type { Editor } from "@tiptap/react";
import { IssueDescriptionEditor } from "@/components/issue/issue-description-editor";
import { useI18n } from "@/i18n/i18n";
import { useUnsavedNavigation } from "@/hooks/use-unsaved-navigation";
import { createAgentSkill, updateAgentSkill } from "@/lib/api";
import { agentSkillsSettingsBackLink } from "@/lib/agent-skills-settings-back-link";
import { toast } from "@/lib/toast";
import type { BootstrapData } from "@/types/flow";
import { AgentSkillActionsMenu } from "./agent-skill-actions";
import { useAgentSkillActions } from "./use-agent-skill-actions";
import { SettingsCrumb } from "./settings-primitives";
import { confirmTemplateAction } from "./template-confirm";
import "./agent-skills.css";

export function AgentSkillEditor({
  data,
  id,
  mode,
  onCancel,
  onReload,
}: {
  data: BootstrapData;
  id?: string;
  mode: "new" | "edit";
  onCancel: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const existing = mode === "edit" ? data.agentSkills.find((item) => item.id === id) : undefined;
  const actions = useAgentSkillActions(data, onReload);
  const [name, setName] = useState(existing?.name ?? "");
  const [instructions, setInstructions] = useState(existing?.instructions ?? "");
  const [saving, setSaving] = useState(false);
  const editorRef = useRef<Editor | null>(null);
  // A skill that is gone (deleted elsewhere) has nothing to edit: return to the list.
  useEffect(() => {
    if (mode === "edit" && !existing) onCancel();
  }, [existing, mode, onCancel]);
  const dirty = existing ? name !== existing.name || instructions.trim() !== existing.instructions.trim() : Boolean(name.trim() || instructions.trim());
  const confirmDiscard = () => confirmTemplateAction({ title: t("Discard changes?"), description: t("Confirm that you want to discard your changes."), confirmLabel: t("Discard"), cancelLabel: t("Cancel") });
  const allowNavigation = useUnsavedNavigation(dirty, confirmDiscard);
  const back = agentSkillsSettingsBackLink(data.workspace.urlKey);
  const save = async () => {
    if (saving) return;
    if (!name.trim()) return void toast.warning(t("Title cannot be empty"));
    if (!instructions.trim()) return void toast.warning(t("Instructions cannot be empty"));
    if (existing && !dirty) return void toast.warning(t("No changes to save"));
    setSaving(true);
    try {
      const input = { name: name.trim(), instructions: instructions.trim() };
      if (existing) await updateAgentSkill(existing.id, input);
      else await createAgentSkill(input);
      await onReload();
      allowNavigation();
      onCancel();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not save skill"));
    } finally {
      setSaving(false);
    }
  };
  const cancel = async () => {
    if (dirty && !(await confirmDiscard())) return;
    allowNavigation();
    onCancel();
  };
  if (mode === "edit" && !existing) return null;
  return (
    <div className="agent-skill-page">
      <SettingsCrumb pinned onClick={() => void cancel()}>{t(back.label)}</SettingsCrumb>
      <div className="agent-skill-title-row">
        <input
          autoFocus={!existing}
          className="agent-skill-title"
          aria-label={t("Skill name")}
          placeholder={t("Skill name")}
          value={name}
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.nativeEvent.isComposing) {
              event.preventDefault();
              if (event.metaKey || event.ctrlKey) void save();
              else editorRef.current?.commands.focus("start");
            }
          }}
        />
        {existing && <AgentSkillActionsMenu skill={existing} actions={actions} hideEdit onDeleted={() => { allowNavigation(); onCancel(); }} />}
      </div>
      <section
        className="agent-skill-card"
        onKeyDown={(event) => {
          if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
            event.preventDefault();
            void save();
          }
        }}
      >
        <IssueDescriptionEditor
          key={existing?.id ?? "new"}
          className="agent-skill-instructions"
          ariaLabel="Skill instructions"
          placeholder="Add instructions…"
          users={data.users}
          value={existing?.instructions ?? ""}
          editorRef={(editor) => { editorRef.current = editor; }}
          onChange={(snapshot) => setInstructions(snapshot.markdown)}
          onSubmit={() => void save()}
        />
      </section>
      <footer className="agent-skill-actions">
        <button type="button" className="agent-skill-cancel" onClick={() => void cancel()}>{t("Cancel")}</button>
        <button type="button" className="agent-skill-save" disabled={saving} onClick={() => void save()}>
          {existing ? t("Save") : t("Create")}
        </button>
      </footer>
    </div>
  );
}
