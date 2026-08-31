import {
  Check,
  Clock3,
  Download,
  GitCompareArrows,
  RotateCcw,
  Upload,
  UserRoundPlus,
  XCircle,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import {
  cancelImport,
  commitImport,
  createExport,
  executeMigration,
  exportDownloadUrl,
  inviteMigrationUsers,
  migrationBundleDownloadUrl,
  migrationManifestDownloadUrl,
  previewImport,
  previewMigration,
  retryImport,
  rollbackMigration,
  scanLinearMigrationTarget,
  updateMigrationMappings,
} from "@/lib/api";
import type {
  BootstrapData,
  ImportJob,
  MigrationEntityMapping,
  MigrationJob,
} from "@/types/flow";
import { SettingsSelect } from "./settings-primitives";

export {
  ProjectUpdateSettings,
  SLASettings,
  TemplateEditor,
  TemplateSettings,
} from "./issues-projects-settings";

export function ImportExportSettings({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const [job, setJob] = useState<ImportJob>();
  const [mapping, setMapping] = useState<Record<string, string>>({});
  const [uploading, setUploading] = useState(false);
  const [exporting, setExporting] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const warnings = useMemo(
    () => (job ? importMappingWarnings(job, mapping, data) : []),
    [data, job, mapping],
  );
  const queuedExports = data.exportJobs.some(
    (item) => item.status === "queued",
  );
  const activeImports = data.importJobs.some(
    (item) => item.status === "running",
  );
  useEffect(() => {
    if (!queuedExports) return;
    const timer = window.setTimeout(() => void onReload(), 700);
    return () => window.clearTimeout(timer);
  }, [data.exportJobs, onReload, queuedExports]);
  useEffect(() => {
    if (!activeImports) return;
    const timer = window.setInterval(() => void onReload(), 700);
    return () => window.clearInterval(timer);
  }, [activeImports, onReload]);
  const pick = async (file?: File) => {
    if (!file) return;
    setUploading(true);
    try {
      const value = await previewImport(file);
      setJob(value);
      const find = (...names: string[]) =>
        value.headers.find((header) => names.includes(header.toLowerCase())) ??
        "";
      setMapping({
        sourceId: find("id", "identifier", "issue id"),
        title: find("title", "name", "summary"),
        description: find("description", "body", "details"),
        priority: find("priority"),
        status: find("status", "state"),
        estimate: find("estimate", "points"),
        assignee: find("assignee", "assignee email", "owner"),
        labels: find("labels", "label", "tags"),
        project: find("project", "project name"),
        dueDate: find("due date", "due_date", "duedate"),
        createdAt: find("created", "created at", "created_at"),
        updatedAt: find("updated", "updated at", "updated_at"),
        startedAt: find("started", "started at", "started_at"),
        triagedAt: find("triaged", "triaged at", "triaged_at"),
        completedAt: find("completed", "completed at", "completed_at"),
        canceledAt: find(
          "canceled",
          "cancelled",
          "canceled at",
          "cancelled at",
        ),
        archivedAt: find("archived", "archived at", "archived_at"),
        parentId: find("parent issue", "parent", "parent id"),
      });
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not parse import",
      );
    } finally {
      setUploading(false);
    }
  };
  const startExport = async (format: "json" | "csv") => {
    setExporting(true);
    try {
      await createExport(format, false);
      await onReload();
    } finally {
      setExporting(false);
    }
  };
  return (
    <>
      <header className="settings-page-header">
        <div>
          <h1>Import &amp; export</h1>
        </div>
      </header>
      <section className="settings-section">
        <h3>Import assistant</h3>
        <div className="settings-card">
          <div className="settings-row">
            <div>
              <strong>CSV or JSON</strong>
              <span>
                Upload issue data, preview parsed rows, then map source columns
                to Flow fields.
              </span>
            </div>
            <button
              className="settings-action"
              disabled={uploading}
              onClick={() => inputRef.current?.click()}
            >
              <Upload size={14} />
              {uploading ? "Parsing…" : "Choose file"}
            </button>
            <input
              ref={inputRef}
              hidden
              type="file"
              accept=".csv,.json,text/csv,application/json"
              onChange={(e) => void pick(e.target.files?.[0])}
            />
          </div>
          {job && job.status === "mapping" && (
            <div className="advanced-import-mapping">
              <div>
                <strong>{job.filename}</strong>
                <small>
                  {job.rows?.length ?? 0} rows · {job.format.toUpperCase()}
                </small>
              </div>
              {(
                [
                  "sourceId",
                  "title",
                  "description",
                  "priority",
                  "status",
                  "estimate",
                  "assignee",
                  "labels",
                  "project",
                  "dueDate",
                  "createdAt",
                  "updatedAt",
                  "startedAt",
                  "triagedAt",
                  "completedAt",
                  "canceledAt",
                  "archivedAt",
                  "parentId",
                ] as const
              ).map((field) => (
                <label key={field}>
                  <span>
                    {field === "dueDate"
                      ? "Due date"
                      : field[0].toUpperCase() + field.slice(1)}
                    {field === "title" && " *"}
                  </span>
                  <SettingsSelect
                    label={`${field} mapping`}
                    value={mapping[field] ?? ""}
                    onChange={(value) =>
                      setMapping((current) => ({
                        ...current,
                        [field]: value,
                      }))
                    }
                    options={[
                      { value: "", label: "Do not import" },
                      ...job.headers.map((header) => ({
                        value: header,
                        label: header,
                        entityName: true,
                      })),
                    ]}
                  />
                </label>
              ))}
              {warnings.length > 0 && (
                <div className="advanced-import-warnings" role="status">
                  <strong>{warnings.length} unmatched values</strong>
                  {warnings.slice(0, 8).map((value) => (
                    <span key={value}>{value}</span>
                  ))}
                  {warnings.length > 8 && (
                    <small>and {warnings.length - 8} more</small>
                  )}
                </div>
              )}
              <button
                className="settings-action primary"
                disabled={!mapping.title}
                onClick={() =>
                  void commitImport(job.id, mapping, data.teams[0].id).then(
                    async (value) => {
                      setJob(value);
                      await onReload();
                    },
                  )
                }
              >{`Import ${job.rows?.length ?? 0} issues`}</button>
            </div>
          )}
          {data.importJobs.slice(0, 5).map((item) => (
            <div className="advanced-job" key={item.id}>
              {item.status === "completed" ? (
                <Check size={14} />
              ) : item.status === "failed" ? (
                <XCircle size={14} />
              ) : (
                <Clock3 size={14} />
              )}
              <span>
                <strong>{item.filename}</strong>
                <small>
                  {item.status === "running"
                    ? `Importing ${item.progress ?? 0}%`
                    : item.status === "mapping"
                      ? "Waiting for mapping"
                      : `${item.imported} imported${item.errors.length ? ` · ${item.errors.length} warnings` : ""}`}
                </small>
              </span>
              {(item.status === "running" || item.status === "mapping") && (
                <button
                  className="settings-action"
                  onClick={() => void cancelImport(item.id).then(onReload)}
                >
                  <XCircle size={14} />
                  Cancel
                </button>
              )}
              {(item.status === "failed" || item.status === "cancelled") && (
                <button
                  className="settings-action"
                  onClick={() =>
                    void retryImport(item.id).then((value) => {
                      setJob(value);
                      setMapping(value.mapping ?? {});
                      return onReload();
                    })
                  }
                >
                  <RotateCcw size={14} />
                  Retry
                </button>
              )}
            </div>
          ))}
        </div>
      </section>
      <MigrationAssistant data={data} onReload={onReload} />
      <section className="settings-section">
        <h3>Export</h3>
        <div className="settings-card">
          <div className="settings-row">
            <div>
              <strong>Complete workspace data</strong>
              <span>
                Create a background export in JSON or an issue-focused CSV.
              </span>
            </div>
            <div className="settings-control">
              <button
                className="settings-action"
                disabled={exporting}
                onClick={() => void startExport("csv")}
              >
                <Download size={14} />
                CSV
              </button>
              <button
                className="settings-action"
                disabled={exporting}
                onClick={() => void startExport("json")}
              >
                <Download size={14} />
                JSON
              </button>
            </div>
          </div>
          {data.exportJobs.slice(0, 5).map((item) => (
            <div className="advanced-job" key={item.id}>
              {item.status === "completed" ? (
                <Check size={14} />
              ) : (
                <Clock3 size={14} />
              )}
              <span>
                <strong>
                  {item.filename ??
                    `Preparing ${item.format.toUpperCase()} export`}
                </strong>
                <small>
                  {item.status === "queued"
                    ? "Queued in background"
                    : new Date(item.createdAt).toLocaleString()}
                </small>
              </span>
              {item.status === "completed" && (
                <a
                  className="settings-action"
                  href={exportDownloadUrl(item.id)}
                  download
                >
                  <Download size={14} />
                  Download
                </a>
              )}
            </div>
          ))}
        </div>
      </section>
    </>
  );
}

function MigrationAssistant({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const [job, setJob] = useState<MigrationJob | undefined>(
    data.migrationJobs?.[0],
  );
  const [target, setTarget] = useState<"flow" | "linear">(
    data.migrationJobs?.[0]?.target ?? "flow",
  );
  const [targetTeamId, setTargetTeamId] = useState(
    data.migrationJobs?.[0]?.targetTeamId ?? data.teams[0]?.id ?? "",
  );
  const [apiToken, setApiToken] = useState("");
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const latest = data.migrationJobs?.[0];
    if (latest && latest.id !== job?.id) {
      setJob(latest);
      setTarget(latest.target);
      setTargetTeamId(latest.targetTeamId ?? data.teams[0]?.id ?? "");
    }
  }, [data.migrationJobs, data.teams, job?.id]);
  const upload = async (file?: File) => {
    if (!file) return;
    setBusy(true);
    try {
      const value = await previewMigration(file);
      setJob(value);
      setTarget(value.target);
      setTargetTeamId(value.targetTeamId ?? data.teams[0]?.id ?? "");
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not read migration bundle",
      );
    } finally {
      setBusy(false);
    }
  };
  const patchMapping = async (
    mapping: MigrationEntityMapping,
    input: Partial<MigrationEntityMapping>,
  ) => {
    if (!job) return;
    const updated = await updateMigrationMappings(job.id, {
      mappings: [
        {
          entityType: mapping.entityType,
          sourceId: mapping.sourceId,
          ...input,
        },
      ],
      target,
      targetTeamId,
    });
    setJob(updated);
  };
  const changeTarget = async (value: "flow" | "linear") => {
    setTarget(value);
    if (job)
      setJob(
        await updateMigrationMappings(job.id, {
          mappings: [],
          target: value,
          targetTeamId,
        }),
      );
  };
  const execute = async () => {
    if (!job) return;
    setBusy(true);
    try {
      const updated = await executeMigration(job.id, {
        target,
        targetTeamId,
        apiToken: target === "linear" ? apiToken : undefined,
      });
      setJob(updated);
      setApiToken("");
      await onReload();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Migration failed");
      await onReload();
    } finally {
      setBusy(false);
    }
  };
  const unresolved =
    job?.mappings.filter((item) => item.action === "review").length ?? 0;
  const pendingInvites =
    target === "flow"
      ? (job?.mappings.filter(
          (item) => item.action === "invite" && item.status !== "invited",
        ).length ?? 0)
      : 0;
  return (
    <section className="settings-section">
      <h3>Workspace migration</h3>
      <div className="settings-card migration-assistant">
        <div className="settings-row">
          <div>
            <strong>Migration bundle</strong>
            <span>
              Map users and planning entities, then restore issues, comments,
              relations, attachments, subscribers, releases, customers, and SLA
              metadata in dependency order.
            </span>
          </div>
          <div className="settings-control">
            <a
              className="settings-action"
              href={migrationBundleDownloadUrl(data.workspace.urlKey)}
              download
            >
              <Download size={14} />
              Export bundle
            </a>
            <button
              className="settings-action"
              disabled={busy}
              onClick={() => inputRef.current?.click()}
            >
              <Upload size={14} />
              Import bundle
            </button>
            <input
              ref={inputRef}
              hidden
              type="file"
              accept=".json,application/json"
              onChange={(event) => void upload(event.target.files?.[0])}
            />
          </div>
        </div>
        {job && (
          <>
            <div className="migration-assistant__summary">
              <span>
                <GitCompareArrows /> {job.filename}
              </span>
              {Object.entries(job.counts)
                .filter(([, count]) => count > 0)
                .map(([kind, count]) => (
                  <b key={kind}>
                    {kind} {count}
                  </b>
                ))}
            </div>
            <div className="migration-assistant__target">
              <label>
                Target
                <SettingsSelect
                  label="Target"
                  value={target}
                  onChange={(value) =>
                    void changeTarget(value as "flow" | "linear")
                  }
                  options={[
                    { value: "flow", label: "This Flow workspace" },
                    { value: "linear", label: "Linear workspace" },
                  ]}
                />
              </label>
              {target === "flow" && (
                <label>
                  Target team
                  <SettingsSelect
                    label="Target team"
                    value={targetTeamId}
                    onChange={setTargetTeamId}
                    options={data.teams.map((team) => ({
                      value: team.id,
                      label: team.name,
                      entityName: true,
                    }))}
                  />
                </label>
              )}
              {target === "linear" && (
                <label>
                  Linear team ID
                  <input
                    data-i18n-ignore
                    value={targetTeamId}
                    onChange={(event) => setTargetTeamId(event.target.value)}
                    placeholder="Linear team UUID or key"
                  />
                </label>
              )}
              {target === "linear" && (
                <label>
                  Temporary Linear API key
                  <input
                    data-i18n-ignore
                    type="password"
                    autoComplete="off"
                    value={apiToken}
                    onChange={(event) => setApiToken(event.target.value)}
                    placeholder="lin_api_…"
                  />
                </label>
              )}
              {target === "linear" && (
                <button
                  className="settings-action"
                  disabled={!apiToken || !targetTeamId || busy}
                  onClick={() =>
                    void scanLinearMigrationTarget(job.id, {
                      apiToken,
                      targetTeamId,
                    }).then(setJob)
                  }
                >
                  Scan Linear mappings
                </button>
              )}
            </div>
            <div className="migration-assistant__mappings">
              {job.mappings
                .filter((item) =>
                  ["user", "team", "project"].includes(item.entityType),
                )
                .map((mapping) => (
                  <div key={`${mapping.entityType}:${mapping.sourceId}`}>
                    <span>
                      <em>{mapping.entityType}</em>
                      <strong>{mapping.sourceName || mapping.sourceId}</strong>
                      {mapping.targetName && (
                        <small>→ {mapping.targetName}</small>
                      )}
                    </span>
                    <SettingsSelect
                      label={`${mapping.sourceName || mapping.sourceId} mapping action`}
                      value={mapping.action}
                      onChange={(value) =>
                        void patchMapping(mapping, {
                          action: value as MigrationEntityMapping["action"],
                        })
                      }
                      options={[
                        { value: "review", label: "Needs mapping" },
                        { value: "map", label: "Map existing" },
                        ...(mapping.entityType === "user"
                          ? [{ value: "invite", label: "Invite user" }]
                          : [{ value: "create", label: "Create" }]),
                        { value: "skip", label: "Skip" },
                        { value: "metadata", label: "Metadata only" },
                      ]}
                    />
                    {target === "flow" && mapping.action === "map" && (
                      <SettingsSelect
                        label={`Select target for ${mapping.sourceName || mapping.sourceId}`}
                        value={mapping.targetId ?? ""}
                        onChange={(value) => {
                          const options =
                            mapping.entityType === "user"
                              ? data.users
                              : mapping.entityType === "project"
                                ? data.projects
                                : data.teams;
                          const selected = options.find(
                            (item) => item.id === value,
                          );
                          void patchMapping(mapping, {
                            targetId: value,
                            targetName:
                              "displayName" in (selected ?? {})
                                ? (selected as { displayName: string })
                                    .displayName
                                : (selected as { name?: string } | undefined)
                                    ?.name,
                          });
                        }}
                        options={[
                          { value: "", label: "Select target…" },
                          ...(mapping.entityType === "user"
                            ? data.users
                            : mapping.entityType === "project"
                              ? data.projects
                              : data.teams
                          ).map((item) => ({
                            value: item.id,
                            label:
                              "displayName" in item
                                ? item.displayName
                                : item.name,
                            entityName: true,
                          })),
                        ]}
                      />
                    )}
                  </div>
                ))}
            </div>
            <footer className="migration-assistant__footer">
              <span>
                {job.status} · {job.phase} · {job.progress}%
                {unresolved ? ` · ${unresolved} mappings unresolved` : ""}
              </span>
              {job.status === "completed" && (
                <a
                  className="settings-action"
                  href={migrationManifestDownloadUrl(
                    job.id,
                    data.workspace.urlKey,
                  )}
                  download
                >
                  Manifest
                </a>
              )}
              {job.mappings.some(
                (item) => item.action === "invite" && item.status !== "invited",
              ) &&
                target === "flow" && (
                  <button
                    className="settings-action"
                    onClick={() =>
                      void inviteMigrationUsers(job.id).then(setJob)
                    }
                  >
                    <UserRoundPlus size={14} />
                    Send invitations
                  </button>
                )}
              {job.status === "completed" && (
                <button
                  className="settings-action danger"
                  disabled={target === "linear" && !apiToken}
                  onClick={() =>
                    void rollbackMigration(
                      job.id,
                      target === "linear" ? apiToken : undefined,
                    ).then(async (value) => {
                      setJob(value);
                      await onReload();
                    })
                  }
                >
                  Rollback
                </button>
              )}
              <button
                className="settings-action primary"
                disabled={
                  busy ||
                  unresolved > 0 ||
                  pendingInvites > 0 ||
                  !targetTeamId ||
                  (target === "linear" && !apiToken)
                }
                onClick={() => void execute()}
              >
                {busy ? "Migrating…" : "Start migration"}
              </button>
            </footer>
            {job.errors.length > 0 && (
              <div className="advanced-import-warnings">
                <strong>{job.errors.length} migration warnings</strong>
                {job.errors.slice(0, 10).map((error, index) => (
                  <span key={`${index}:${error}`}>{error}</span>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}

function importMappingWarnings(
  job: ImportJob,
  mapping: Record<string, string>,
  data: BootstrapData,
) {
  const warnings = new Set<string>();
  const rows = job.rows ?? [];
  const values = (field: string) => [
    ...new Set(
      rows
        .map((row) => row[mapping[field]]?.trim())
        .filter(Boolean) as string[],
    ),
  ];
  const known = (
    value: string,
    options: {
      id: string;
      name?: string;
      email?: string;
      displayName?: string;
      slugId?: string;
    }[],
  ) =>
    options.some(
      (item) =>
        item.id === value ||
        [item.name, item.email, item.displayName, item.slugId].some(
          (label) => label?.toLowerCase() === value.toLowerCase(),
        ),
    );
  for (const value of values("priority"))
    if (!/^(0|1|2|3|4|none|no priority|urgent|high|medium|low)$/i.test(value))
      warnings.add(`Priority: ${value}`);
  for (const value of values("status"))
    if (!known(value, data.states)) warnings.add(`Status: ${value}`);
  for (const value of values("assignee"))
    if (!known(value, data.users)) warnings.add(`Assignee: ${value}`);
  for (const value of values("project"))
    if (!known(value, data.projects)) warnings.add(`Project: ${value}`);
  for (const source of values("labels"))
    for (const value of source
      .split(/[,;]/)
      .map((part) => part.trim())
      .filter(Boolean))
      if (!known(value, data.labels)) warnings.add(`Label: ${value}`);
  for (const value of values("dueDate"))
    if (
      !/^\d{4}-\d{2}-\d{2}$/.test(value) ||
      Number.isNaN(Date.parse(`${value}T00:00:00Z`))
    )
      warnings.add(`Due date: ${value}`);
  return [...warnings];
}
