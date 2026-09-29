import { useEffect, useState } from "react";
import { History, RotateCcw } from "lucide-react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { UserAvatar } from "@/components/ui/user-avatar";
import { listLoopVersions, restoreLoopVersion } from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopVersion, LoopVersionChange } from "@/types/flow";
import { LoopInstructionsEditor } from "./loop-instructions-editor";
import { relativeTime } from "./loop-model";
import { LoopTriggerEditor } from "./loop-trigger";

type Translate = (source: string) => string;

const CHANGE_LABELS: Record<Exclude<LoopVersionChange, "published" | "restored">, string> = {
  name: "Name",
  trigger: "Trigger",
  instructions: "Instructions",
  connectors: "Connectors",
  teamAccess: "Team access",
  allowChangesOutsideTrigger: "Changes outside trigger",
  allowExternalSync: "Externally synced changes",
  webSearch: "Web search",
  codeAccess: "Access code",
  level: "Location",
};

/** "Published", "Restored version 2", "Changed trigger, instructions". */
export function versionSummary(version: Pick<LoopVersion, "changeSummary" | "restoredFromVersion">, t: Translate = (value) => value) {
  const changes = version.changeSummary ?? [];
  if (changes.includes("restored")) return t("Restored version {version}").replace("{version}", String(version.restoredFromVersion ?? "?"));
  if (changes.includes("published") || !changes.length) return t("Published");
  const fields = changes.filter((item): item is keyof typeof CHANGE_LABELS => item in CHANGE_LABELS).map((item) => t(CHANGE_LABELS[item]).toLocaleLowerCase());
  return t("Changed {fields}").replace("{fields}", fields.join(", "));
}

const stable = (value: unknown) => JSON.stringify(value ?? null, (_key, item) => (item && typeof item === "object" && !Array.isArray(item) ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b))) : item));

/** Definition fields where a version differs from the loop as it is now (same groups as `changeSummary`). */
export function versionDiff(definition: LoopVersion["definition"], loop: Loop): (keyof typeof CHANGE_LABELS)[] {
  const changed: (keyof typeof CHANGE_LABELS)[] = [];
  if ((definition.name ?? "") !== (loop.name ?? "")) changed.push("name");
  if (definition.triggerType !== loop.triggerType || stable(definition.triggerConfig) !== stable(loop.triggerConfig)) changed.push("trigger");
  if ((definition.instructions ?? "").trim() !== (loop.instructions ?? "").trim()) changed.push("instructions");
  if (stable([...(definition.connectorIds ?? [])].sort()) !== stable([...(loop.connectorIds ?? [])].sort())) changed.push("connectors");
  if (definition.teamAccess !== loop.teamAccess) changed.push("teamAccess");
  if (Boolean(definition.allowChangesOutsideTrigger) !== Boolean(loop.allowChangesOutsideTrigger)) changed.push("allowChangesOutsideTrigger");
  if (Boolean(definition.allowExternalSync) !== Boolean(loop.allowExternalSync)) changed.push("allowExternalSync");
  if (Boolean(definition.webSearch) !== Boolean(loop.webSearch)) changed.push("webSearch");
  if ((definition.codeAccess ?? "read") !== (loop.codeAccess ?? "read")) changed.push("codeAccess");
  if (definition.level !== loop.level || (definition.level === "team" && definition.teamId !== loop.teamId)) changed.push("level");
  return changed;
}

function VersionDefinition({ data, version, loop, onNavigate }: { data: BootstrapData; version: LoopVersion; loop: Loop; onNavigate: (path: string) => void }) {
  const { t } = useI18n();
  const definition = version.definition;
  const diff = versionDiff(definition, loop);
  const team = definition.level === "team" ? data.teams.find((item) => item.id === definition.teamId) : undefined;
  const codeAccess = { disabled: "Disabled", read: "Read", readWrite: "Read & write" }[definition.codeAccess ?? "read"];
  const onOff = (value: boolean | undefined) => t(value ? "Enabled" : "Disabled");
  return (
    <div className="loops-version-definition">
      <p className={`loops-version-diff${diff.length ? " is-changed" : ""}`} role="status">
        {diff.length
          ? t("Differs from the current loop: {fields}").replace("{fields}", diff.map((item) => t(CHANGE_LABELS[item]).toLocaleLowerCase()).join(", "))
          : t("Same as the current loop")}
      </p>
      <section className="loops-card is-trigger" aria-label={t("Trigger")}>
        <h3>{t("Trigger")}</h3>
        <LoopTriggerEditor data={data} triggerType={definition.triggerType} config={definition.triggerConfig ?? {}} readOnly />
      </section>
      <section className="loops-card is-instructions" aria-label={t("Instructions")}>
        <h3>{t("Instructions")}</h3>
        <div className="loops-instructions-text" data-i18n-ignore>
          {definition.instructions ? (
            <LoopInstructionsEditor readOnly ariaLabel={t("Version instructions")} data={data} value={definition.instructions} valueData={definition.instructionsData} onNavigate={onNavigate} />
          ) : (
            <span className="loops-muted">{t("No instructions")}</span>
          )}
        </div>
      </section>
      <section className="loops-card loops-permissions" aria-label={t("Permissions")}>
        <h3>{t("Permissions")}</h3>
        <dl className="loops-version-permissions">
          <dt>{t("Location")}</dt>
          <dd data-i18n-ignore={team ? true : undefined}>{team ? team.name : t("Workspace")}</dd>
          <dt>{t("Team access")}</dt>
          <dd>{t(definition.teamAccess === "selected" ? "Selected teams" : "All public teams")}</dd>
          {definition.triggerType !== "schedule" && (
            <>
              <dt>{t("Changes outside trigger")}</dt>
              <dd>{onOff(definition.allowChangesOutsideTrigger)}</dd>
            </>
          )}
          <dt>{t("Web search")}</dt>
          <dd>{onOff(definition.webSearch)}</dd>
          <dt>{t("Access code")}</dt>
          <dd>{t(codeAccess)}</dd>
          <dt>{t("Externally synced changes")}</dt>
          <dd>{onOff(definition.allowExternalSync)}</dd>
        </dl>
      </section>
    </div>
  );
}

/** "Show published versions": versions newest first, a read-only definition and "Restore this version". */
export function LoopVersionsDialog({
  data,
  loop,
  open,
  onOpenChange,
  onRestored,
  onNavigate,
}: {
  data: BootstrapData;
  loop: Loop;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRestored: (loop: Loop) => void;
  onNavigate: (path: string) => void;
}) {
  const { t } = useI18n();
  const [versions, setVersions] = useState<LoopVersion[]>();
  const [selectedId, setSelectedId] = useState<string>();
  const [error, setError] = useState<string>();
  const [restoring, setRestoring] = useState(false);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    if (!open) return;
    let active = true;
    setError(undefined);
    listLoopVersions(loop.id)
      .then((items) => {
        if (!active) return;
        const list = Array.isArray(items) ? [...items].sort((a, b) => b.version - a.version) : [];
        setVersions(list);
        setSelectedId((current) => (current && list.some((item) => item.id === current) ? current : list[0]?.id));
      })
      .catch((reason) => {
        if (!active) return;
        setVersions([]);
        setError(reason instanceof Error ? reason.message : t("Could not load versions"));
      });
    return () => {
      active = false;
    };
  }, [loop.id, open, reload, t]);

  const selected = versions?.find((item) => item.id === selectedId);
  const restore = async () => {
    if (!selected || restoring) return;
    const confirmed = await confirmAction(t("Restore version {version}?").replace("{version}", String(selected.version)), {
      description: t("The loop's trigger, instructions and permissions are replaced with this version. It is saved as a new version; run history and the enabled state stay as they are."),
      confirmLabel: t("Restore"),
      danger: false,
    });
    if (!confirmed) return;
    setRestoring(true);
    try {
      const next = await restoreLoopVersion(loop.id, selected.id);
      onRestored(next);
      toast.success(t("Version {version} restored").replace("{version}", String(selected.version)));
      setSelectedId(undefined);
      setReload((value) => value + 1);
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : t("Could not restore version"));
    } finally {
      setRestoring(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="loops-versions-dialog" closeLabel={t("Close")} aria-describedby={undefined}>
        <DialogTitle className="loops-versions-title">
          <History size={14} />
          {t("Published versions")}
        </DialogTitle>
        <div className="loops-versions-layout">
          <ol className="loops-versions-list" aria-label={t("Versions")}>
            {versions === undefined ? null : versions.length === 0 ? (
              <li className="loops-run-list-empty">{error ?? t("This loop has no published versions yet.")}</li>
            ) : (
              versions.map((version) => {
                const author = version.publishedBy;
                const authorName = author ? author.displayName || author.name : t("Unknown user");
                return (
                  <li key={version.id}>
                    <button
                      type="button"
                      className={`loops-version-row${version.id === selectedId ? " is-active" : ""}`}
                      aria-current={version.id === selectedId ? "true" : undefined}
                      onClick={() => setSelectedId(version.id)}
                    >
                      <span className="loops-version-row-head">
                        <strong>{t("Version {version}").replace("{version}", String(version.version))}</strong>
                        {version.current && <span className="loops-version-badge">{t("Current")}</span>}
                        <time dateTime={version.publishedAt} title={new Date(version.publishedAt).toLocaleString()}>
                          {relativeTime(version.publishedAt, undefined, t)}
                        </time>
                      </span>
                      <span className="loops-version-row-meta">
                        <UserAvatar avatarUrl={author?.avatarUrl} className="avatar loops-avatar" name={authorName} />
                        <span data-i18n-ignore>{authorName}</span>
                        <span aria-hidden="true">·</span>
                        <span>{versionSummary(version, t)}</span>
                      </span>
                    </button>
                  </li>
                );
              })
            )}
          </ol>
          <section className="loops-versions-detail" aria-label={selected ? t("Version {version}").replace("{version}", String(selected.version)) : t("Version")}>
            {selected && (
              <>
                <header className="loops-versions-detail-header">
                  <div>
                    <h3 data-i18n-ignore>{selected.definition.name || loop.name}</h3>
                    <p>
                      {t("Version {version}").replace("{version}", String(selected.version))} · {versionSummary(selected, t)}
                    </p>
                  </div>
                  <button className="loops-secondary-button" type="button" disabled={selected.current || restoring} title={selected.current ? t("This is the current version") : undefined} onClick={() => void restore()}>
                    <RotateCcw size={13} />
                    {t("Restore this version")}
                  </button>
                </header>
                <VersionDefinition data={data} version={selected} loop={loop} onNavigate={onNavigate} />
              </>
            )}
          </section>
        </div>
      </DialogContent>
    </Dialog>
  );
}
