import { useMemo, useState, type FormEvent } from "react";

import { TeamIcon } from "@/components/issue/issue-icons";
import { ViewIconPicker } from "@/components/views/view-icon-picker";
import { useI18n } from "@/i18n/i18n";
import { teamHierarchy } from "@/lib/team-hierarchy";
import { suggestTeamKey } from "@/lib/team-key";
import { defaultTeamTimezone, timezoneOptions } from "@/lib/team-timezones";
import { toast } from "@/lib/toast";
import type { BootstrapData } from "@/types/flow";
import {
  SettingsCrumb,
  SettingsPageTitle,
  SettingsRow,
  SettingsSection,
  SettingsSelect,
} from "./settings-primitives";
import "./team-create-settings.css";

export type TeamCreateInput = {
  name: string;
  key: string;
  color?: string;
  icon?: string;
  private?: boolean;
  parentTeamId?: string;
  copyFromTeamId?: string;
  timezone?: string;
};

const MAX_KEY_LENGTH = 5;

/** Settings › Create a new team: the form lives inside the shared settings layout. */
export function TeamCreateSettings({
  data,
  parentTeamId: initialParentTeamId = "",
  initialName = "",
  onBack,
  onCreate,
  onCreated,
}: {
  data: BootstrapData;
  parentTeamId?: string;
  initialName?: string;
  onBack: () => void;
  onCreate: (input: TeamCreateInput) => Promise<void>;
  onCreated: (key: string) => void;
}) {
  const { t } = useI18n();
  const teams = data.teams;
  const activeTeams = useMemo(() => teams.filter((team) => !team.retiredAt), [teams]);
  const hierarchy = useMemo(() => teamHierarchy(teams, data.teamSettings), [teams, data.teamSettings]);
  const takenKeys = useMemo(() => teams.map((team) => team.key), [teams]);
  const [name, setName] = useState(initialName);
  const [key, setKey] = useState(() => suggestTeamKey(initialName, takenKeys));
  const [keyFollowsName, setKeyFollowsName] = useState(true);
  const [color, setColor] = useState("#bec2c8");
  const [icon, setIcon] = useState("Team");
  const [parentTeamId, setParentTeamId] = useState(hierarchy.byId.has(initialParentTeamId) ? initialParentTeamId : "");
  const [privateTeam, setPrivateTeam] = useState(false);
  const [timezone, setTimezone] = useState(() =>
    defaultTeamTimezone(teams.map((team) => data.teamSettings?.[team.id]?.timezone ?? "")),
  );
  const [copyFromId, setCopyFromId] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [saving, setSaving] = useState(false);
  const [serverError, setServerError] = useState("");
  const timezones = useMemo(() => timezoneOptions(), []);

  const parent = hierarchy.byId.get(parentTeamId);
  const copySourceId = parentTeamId || copyFromId;
  const restrictedRoot = parent
    ? [parent, ...(hierarchy.ancestors.get(parentTeamId) ?? [])].find(
        (team) => team.private || ["private", "restricted"].includes(data.teamSettings?.[team.id]?.access ?? ""),
      )
    : undefined;
  const accessValue = privateTeam ? "private" : restrictedRoot ? "restricted" : "public";

  const trimmedName = name.trim();
  const identifier = key.trim().toUpperCase();
  const nameError = !submitted
    ? ""
    : !trimmedName
      ? t("Name must be at least 1 character long")
      : teams.some((team) => team.name.toLowerCase() === trimmedName.toLowerCase())
        ? t("Team with this name already exists")
        : "";
  const keyOwner = teams.find((team) => team.key.toUpperCase() === identifier);
  const keyError = !submitted
    ? ""
    : !identifier
      ? t("Identifier must be at least 1 character long")
      : keyOwner
        ? `The team ${keyOwner.name} is already using this identifier`
        : "";
  const parentError = parentTeamId ? hierarchy.parentError("", parentTeamId) : null;

  const changeName = (value: string) => {
    setName(value);
    if (keyFollowsName) setKey(suggestTeamKey(value, takenKeys));
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving) return;
    setSubmitted(true);
    setServerError("");
    if (!trimmedName || !identifier || keyOwner || teams.some((team) => team.name.toLowerCase() === trimmedName.toLowerCase())) return;
    if (parentError) {
      toast.error(t("Invalid parent team selected"), { description: t("Select a valid parent team to continue") });
      return;
    }
    setSaving(true);
    try {
      await onCreate({
        name: trimmedName,
        key: identifier,
        color,
        icon,
        private: privateTeam,
        parentTeamId,
        copyFromTeamId: copySourceId,
        timezone,
      });
      onCreated(identifier);
    } catch (error) {
      setServerError(error instanceof Error ? error.message : t("Could not create team"));
    } finally {
      setSaving(false);
    }
  };

  const teamOption = (team: (typeof teams)[number], label: string) => ({
    value: team.id,
    label,
    entityName: true,
    keywords: `${team.key} ${label}`,
    icon: <TeamIcon team={team} size={16} />,
  });
  const parentOptions = [
    ...(parentTeamId ? [{ value: "", label: t("No parent team") }] : []),
    ...hierarchy
      .rows(activeTeams)
      .filter(({ team }) => !hierarchy.parentError("", team.id) || team.id === parentTeamId)
      .map(({ team }) => teamOption(team, hierarchy.path(team.id))),
  ];
  const copyOptions = [
    ...(copySourceId && !parentTeamId ? [{ value: "", label: t("Don’t copy") }] : []),
    ...activeTeams.map((team) => teamOption(team, team.name)),
  ];
  const accessOptions = [
    ...(restrictedRoot && !privateTeam
      ? [{ value: "restricted", label: `${t("Restricted to")} ${restrictedRoot.name}`, suffix: t("members"), entityName: true }]
      : [{ value: "public", label: t("Public"), suffix: t("to workspace") }]),
    { value: "private", label: t("Private"), suffix: t("to team members") },
  ];

  return (
    <div className="team-create-settings">
      <SettingsCrumb pinned onClick={onBack}>
        {t("Back")}
      </SettingsCrumb>
      <SettingsPageTitle
        className="team-settings-header"
        description={t("Create a new team to manage separate cycles, workflows, and notifications")}
      >
        {t("Create a new team")}
      </SettingsPageTitle>
      <form className="team-create-form" onSubmit={submit} noValidate>
        <SettingsSection>
          <SettingsRow title={t("Icon & Name")} description={nameError || undefined} className={`team-create-stack-row team-create-name-row${nameError ? " has-error" : ""}`}>
            <div className="team-icon-name-control">
              <ViewIconPicker
                align="end"
                color={color}
                icon={icon}
                onChange={(visual) => {
                  setColor(visual.color);
                  setIcon(visual.icon);
                }}
                prependTeam
                triggerClassName="team-icon-trigger"
              />
              <input
                autoFocus
                aria-label={t("Icon & Name")}
                autoComplete="off"
                className="settings-input"
                placeholder={t("e.g. Engineering")}
                value={name}
                onChange={(event) => changeName(event.target.value)}
              />
            </div>
          </SettingsRow>
          <SettingsRow
            title={t("Identifier")}
            description={keyError || t("Used as a prefix for team issues (e.g. ENG-123)").replace("ENG-123", `${identifier || "ENG"}-123`)}
            className={`team-create-stack-row${keyError ? " has-error" : ""}`}
          >
            <input
              aria-label={t("Identifier")}
              autoComplete="off"
              className="settings-input"
              maxLength={MAX_KEY_LENGTH}
              placeholder={t("e.g. ENG")}
              value={key}
              onChange={(event) => {
                setKeyFollowsName(false);
                setKey(event.target.value.replace(/[^a-zA-Z0-9]/g, "").toUpperCase());
              }}
            />
          </SettingsRow>
          <SettingsRow
            title={t("Parent team")}
            description={parentError ?? t("Settings and workflows will be inherited from the parent team")}
            className={`team-create-inline-row${parentError ? " has-error" : ""}`}
          >
            <SettingsSelect
              label={t("Parent team")}
              placeholder={t("No parent team")}
              searchPlaceholder={t("Set parent team…")}
              value={parentTeamId}
              options={parentOptions}
              onChange={(value) => {
                setParentTeamId(value);
                setCopyFromId("");
              }}
            />
          </SettingsRow>
        </SettingsSection>
        <SettingsSection
          headerClassName="team-settings-section-title"
          title={t("Team access")}
          description={t(
            "Control who can access the team and its content. Private teams are visible only to team members and workspace admins.",
          )}
        >
          <SettingsRow title={t("Team access")}>
            <SettingsSelect
              label={t("Team access")}
              value={accessValue}
              options={accessOptions}
              onChange={(value) => setPrivateTeam(value === "private")}
            />
          </SettingsRow>
        </SettingsSection>
        <SettingsSection
          headerClassName="team-settings-section-title"
          title={t("Timezone")}
          description={t("Used for team schedules, dates, and cycle start times")}
        >
          <SettingsRow title={t("Timezone")} className="team-create-stack-row">
            <SettingsSelect
              label={t("Timezone")}
              searchPlaceholder={t("Search timezones…")}
              value={timezone}
              options={timezones.map((option) => ({ value: option.value, label: option.label, keywords: option.keywords, entityName: true }))}
              onChange={setTimezone}
            />
          </SettingsRow>
        </SettingsSection>
        <SettingsSection
          headerClassName="team-settings-section-title"
          title={t("Copy settings from existing team")}
          description={t(
            "Copy workflows, cycle, and team settings from another team. Team members and Slack notification settings won't be copied.",
          )}
        >
          <SettingsRow title={t("Copy from team")} className="team-create-inline-row">
            <span title={parentTeamId ? t("Sub-teams must copy settings from their parent") : undefined}>
              <SettingsSelect
                label={t("Copy from team")}
                disabled={Boolean(parentTeamId)}
                placeholder={t("Don’t copy")}
                searchPlaceholder={t("Select a team…")}
                value={copySourceId}
                options={copyOptions}
                onChange={setCopyFromId}
              />
            </span>
          </SettingsRow>
        </SettingsSection>
        {serverError && (
          <p className="team-create-error" role="alert">
            {serverError}
          </p>
        )}
        <footer className="team-create-footer">
          <button className="settings-action primary" type="submit" aria-busy={saving || undefined} disabled={saving}>
            {t("Create team")}
          </button>
        </footer>
      </form>
    </div>
  );
}
