import { useEffect, useRef, useState } from "react";
import { AlertTriangle, Plus, RotateCcw, X } from "lucide-react";
import { toast } from "@/lib/toast";
import { SelectControl } from "@/components/ui/select-control";
import { Toggle } from "@/components/ui/toggle";
import { analyzeLoopOutputs, updateLoop } from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import { activeTeams } from "./loop-data";
import { LoopDocumentPicker } from "./loop-document-picker";
import { LoopContractReview } from "./loop-contract-review";
import {
  CARDINALITY_LABELS,
  CONTRACT_FALLBACK_NOTICE,
  CONTRACT_KIND_LABELS,
  CONTRACT_OPERATION_LABELS,
  blankOutput,
  contractReady,
  contractTargetText,
  createsOutput,
  operationsFor,
  personContract,
  syncPlaceholders,
  targetChoice,
  targetChoicesFor,
  unchosenDocument,
  withTargetChoice,
  type ContractTargetChoice,
} from "./loop-output-contract-model";
import type { BootstrapData, Loop, LoopContractOutput, LoopOutputContract, LoopOutputKind, LoopOutputOperation } from "@/types/flow";

/** The contract's outputs, read-only: what each run makes, reads and must not touch, with the instruction text behind it. */
export function LoopContractSummary({ contract, data, showEvidence = true }: { contract: LoopOutputContract; data: Pick<BootstrapData, "teams" | "documents"> & Partial<Pick<BootstrapData, "projects" | "initiatives">>; showEvidence?: boolean }) {
  const { t } = useI18n();
  const outputs = contract.readOnly ? contract.outputs.filter((output) => output.operation === "forbidden" || output.operation === "read") : contract.outputs;
  return (
    <div className="loops-contract-summary">
      {contract.readOnly && (
        <p className="loops-contract-readonly">
          <strong>{t("Reply only: the run changes nothing")}</strong>
          {showEvidence && contract.readOnlyEvidence && <q className="loops-contract-evidence" data-i18n-ignore>{contract.readOnlyEvidence}</q>}
        </p>
      )}
      {outputs.length > 0 && (
        <ul className="loops-contract-list" aria-label={t("Outputs")}>
          {outputs.map((output, index) => {
            const target = contractTargetText(output, data, t);
            return (
              <li key={`${output.kind}-${output.operation}-${index}`} className={`loops-contract-item is-${output.operation}`}>
                <span className="loops-contract-head">
                  <span className="loops-contract-operation">{t(CONTRACT_OPERATION_LABELS[output.operation] ?? output.operation)}</span>
                  <span className="loops-contract-kind">{t(CONTRACT_KIND_LABELS[output.kind] ?? output.kind)}</span>
                  {output.required && <span className="loops-contract-badge">{t("Must produce")}</span>}
                </span>
                {target && <small className="loops-contract-target" data-i18n-ignore>{target}</small>}
                {showEvidence && output.evidence && <q className="loops-contract-evidence" data-i18n-ignore>{output.evidence}</q>}
              </li>
            );
          })}
        </ul>
      )}
      {!contract.readOnly && outputs.length === 0 && <p className="loops-contract-empty">{t("No outputs")}</p>}
      {contract.always && <p className="loops-contract-note">{t("Required even when nothing changed")}{showEvidence && contract.alwaysEvidence && <q className="loops-contract-evidence" data-i18n-ignore>{contract.alwaysEvidence}</q>}</p>}
      {(contract.skipConditions ?? []).map((skip) => (
        <p className="loops-contract-note" key={skip.condition}>
          {t("Skips the run: {condition}").replace("{condition}", () => skip.evidence || skip.condition)}
        </p>
      ))}
    </div>
  );
}

const TARGET_CHOICE_LABELS: Record<ContractTargetChoice, string> = {
  any: "Anywhere",
  triggering: "The triggering item",
  matching: "Issues matching a description",
  project: "A project",
  initiative: "An initiative",
};

/** "Choose document…" button over the server-backed document picker. */
function ContractDocumentButton({ output, data, loop, onPick }: { output: LoopContractOutput; data: BootstrapData; loop: Loop; onPick: (document: { id: string; title: string }) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const target = output.target ?? {};
  const chosen = target.documentId ? (data.documents?.find((document) => document.id === target.documentId)?.title ?? target.title) : undefined;
  return (
    <>
      <button type="button" className={`select-control loops-contract-document${unchosenDocument(output) ? " is-unchosen" : ""}`} aria-label={t("Document")} onClick={() => setOpen(true)}>
        <span data-i18n-ignore={chosen ? true : undefined}>{chosen || (target.title && !target.ambiguous ? t("Find by title: {title}").replace("{title}", () => target.title!) : t("Choose document…"))}</span>
      </button>
      <LoopDocumentPicker data={data} open={open} teamId={target.teamId ?? loop.teamId} newTitle={target.description || loop.name} onOpenChange={setOpen} onSelect={(document) => onPick({ id: document.id, title: document.title })} />
    </>
  );
}

/** Editable row of one output. */
function ContractOutputEditor({ output, data, loop, onChange, onRemove }: { output: LoopContractOutput; data: BootstrapData; loop: Loop; onChange: (next: LoopContractOutput) => void; onRemove: () => void }) {
  const { t } = useI18n();
  const target = output.target ?? {};
  const setTarget = (patch: Partial<LoopContractOutput["target"]>) => onChange({ ...output, target: { ...target, ...patch } });
  const writes = output.operation === "create" || output.operation === "update" || output.operation === "append" || output.operation === "write";
  const isDocument = output.kind === "document";
  const creates = createsOutput(output);
  const choices = targetChoicesFor(output);
  // "A project" or "An initiative" is chosen before the project or initiative is.
  const [chosen, setChosen] = useState<ContractTargetChoice>();
  const stored = targetChoice(output);
  const choice = chosen && stored === "any" && (chosen === "project" || chosen === "initiative") && choices.includes(chosen) ? chosen : stored;
  const scoped = output.kind === "issue" || choice === "matching";
  const placeholders = creates ? syncPlaceholders(target.titleTemplate, target.placeholders) : [];
  const projects = (data.projects ?? []).filter((project) => !project.archivedAt);
  const initiatives = data.initiatives ?? [];
  return (
    <li className="loops-contract-edit-row">
      <div className="loops-contract-edit-line">
        <SelectControl
          label={t("Output kind")}
          value={output.kind}
          onChange={(value) => {
            const kind = value as LoopOutputKind;
            onChange({ ...output, kind, operation: operationsFor(kind).includes(output.operation) ? output.operation : "create", target: kind === "document" ? target : { mode: target.mode, team: target.team, teamId: target.teamId } });
          }}
          options={(Object.keys(CONTRACT_KIND_LABELS) as LoopOutputKind[]).map((kind) => ({ value: kind, label: t(CONTRACT_KIND_LABELS[kind]) }))}
        />
        <SelectControl
          label={t("Operation")}
          value={output.operation}
          onChange={(value) => {
            const operation = value as LoopOutputOperation;
            const mode = operation === "create" ? "new" : operation === "update" || operation === "append" ? (isDocument ? "existing" : target.mode) : target.mode;
            onChange({ ...output, operation, required: operation === "read" || operation === "forbidden" ? false : output.required, target: { ...target, mode } });
          }}
          options={operationsFor(output.kind).map((operation) => ({ value: operation, label: t(CONTRACT_OPERATION_LABELS[operation]) }))}
        />
        {writes && (
          <label className="loops-contract-check">
            <input type="checkbox" checked={output.required} onChange={(event) => onChange({ ...output, required: event.target.checked })} />
            {t("Must produce")}
          </label>
        )}
        <button type="button" className="loops-icon-button is-plain" aria-label={t("Remove output")} title={t("Remove output")} onClick={onRemove}>
          <X size={14} />
        </button>
      </div>
      {writes && (
        <div className="loops-contract-edit-line">
          {creates && (
            <input
              className="loops-contract-input"
              aria-label={t("Title template")}
              placeholder={isDocument ? t("Title, e.g. Daily summary-{date}") : t("Title, e.g. Error: {message}")}
              value={target.titleTemplate ?? ""}
              onChange={(event) => setTarget({ titleTemplate: event.target.value, placeholders: syncPlaceholders(event.target.value, target.placeholders) })}
            />
          )}
          {isDocument && (output.operation === "update" || output.operation === "append") && (
            <ContractDocumentButton output={output} data={data} loop={loop} onPick={(document) => setTarget({ documentId: document.id, title: document.title, mode: "existing", ambiguous: false })} />
          )}
          {choices.length > 0 && (
            <SelectControl
              label={t("Target")}
              value={choice}
              onChange={(value) => {
                setChosen(value as ContractTargetChoice);
                onChange({ ...output, target: withTargetChoice(output, value as ContractTargetChoice) });
              }}
              options={choices.map((value) => ({ value, label: t(TARGET_CHOICE_LABELS[value]) }))}
            />
          )}
          {choice === "matching" && (
            <input
              className="loops-contract-input"
              aria-label={t("Which issues")}
              placeholder={t("e.g. open bugs without repro steps")}
              value={target.match ?? ""}
              onChange={(event) => setTarget({ match: event.target.value })}
            />
          )}
          {(choice === "project" || scoped) && output.kind !== "change" && !isDocument && (
            <SelectControl
              label={t("Project")}
              value={target.projectId ?? ""}
              onChange={(value) => setTarget({ projectId: value || undefined, project: value ? projects.find((project) => project.id === value)?.name : undefined })}
              options={[{ value: "", label: target.project && !target.projectId ? target.project : choice === "project" ? t("Choose project…") : t("Any project") }, ...projects.map((project) => ({ value: project.id, label: project.name, entityName: true }))]}
            />
          )}
          {choice === "initiative" && (
            <SelectControl
              label={t("Initiative")}
              value={target.initiativeId ?? ""}
              onChange={(value) => setTarget({ initiativeId: value || undefined, initiative: value ? initiatives.find((initiative) => initiative.id === value)?.name : undefined })}
              options={[{ value: "", label: target.initiative && !target.initiativeId ? target.initiative : t("Choose initiative…") }, ...initiatives.map((initiative) => ({ value: initiative.id, label: initiative.name, entityName: true }))]}
            />
          )}
          {(isDocument || scoped) && (
            <SelectControl
              label={t("Team")}
              value={target.teamId ?? ""}
              onChange={(value) => setTarget({ teamId: value || undefined, team: value ? data.teams.find((team) => team.id === value)?.name : undefined })}
              options={[{ value: "", label: target.team ? target.team : isDocument ? t("The loop's team") : t("Any team") }, ...activeTeams(data.teams).map((team) => ({ value: team.id, label: team.name, entityName: true }))]}
            />
          )}
          <SelectControl
            label={t("How many")}
            value={output.cardinality ?? "any"}
            onChange={(value) => onChange({ ...output, cardinality: value as LoopContractOutput["cardinality"] })}
            options={Object.entries(CARDINALITY_LABELS).map(([value, label]) => ({ value, label: t(label) }))}
          />
        </div>
      )}
      {writes && placeholders.length > 0 && (
        <div className="loops-contract-edit-line loops-contract-placeholders">
          {placeholders.map((placeholder) => (
            <label key={placeholder.name} className="loops-contract-placeholder">
              <code data-i18n-ignore>{`{${placeholder.name}}`}</code>
              <input
                className="loops-contract-input"
                aria-label={t("What fills {name}").replace("{name}", () => `{${placeholder.name}}`)}
                placeholder={t("Filled by the run from…")}
                value={placeholder.source ?? ""}
                onChange={(event) => setTarget({ placeholders: placeholders.map((item) => (item.name === placeholder.name ? { ...item, source: event.target.value } : item)) })}
              />
            </label>
          ))}
        </div>
      )}
      {writes && unchosenDocument(output) && (
        <p className="loops-contract-warning" role="alert">
          <AlertTriangle size={14} />
          <span>{t("The instructions don't say which document. Choose it, or create a new one.")}</span>
        </p>
      )}
      {output.evidence && <q className="loops-contract-evidence" data-i18n-ignore>{output.evidence}</q>}
    </li>
  );
}

/**
 * "What this loop will produce": the output contract read from the saved instructions, with the text behind each
 * output. It reads the contract when the loop has none for its current text, and a person can edit it, which makes
 * the edit authoritative, or go back to the automatic one.
 */
export function LoopOutputContractPanel({
  data,
  loop,
  unsaved,
  onLoopChange,
  readDelayMs = 1500,
}: {
  data: BootstrapData;
  loop: Loop;
  /** The editor has changes to the name or instructions that are not saved yet. */
  unsaved: boolean;
  onLoopChange: (loop: Loop) => void;
  readDelayMs?: number;
}) {
  const { t } = useI18n();
  const contract = loop.outputContract;
  const [analyzing, setAnalyzing] = useState(false);
  const [editing, setEditing] = useState<LoopOutputContract | undefined>(undefined);
  const [saving, setSaving] = useState(false);
  const [reviewing, setReviewing] = useState(false);
  const requested = useRef("");
  const hasInstructions = Boolean(loop.instructions.trim());
  const suggestion = loop.suggestedOutputContract;
  const needsRead = hasInstructions && !unsaved && (!contract || contract.stale && (contract.source !== "person" || !suggestion));

  useEffect(() => {
    // One read per saved text (the server caches it by instructions hash), once the text has settled: a draft
    // saves as it is typed.
    const key = `${loop.id}:${loop.updatedAt}`;
    if (!needsRead || requested.current === key) return;
    let active = true;
    const timer = window.setTimeout(() => {
      requested.current = key;
      setAnalyzing(true);
      analyzeLoopOutputs(loop.id)
        .then((next) => {
          if (active) onLoopChange(next);
        })
        .catch(() => undefined)
        .finally(() => setAnalyzing(false));
    }, readDelayMs);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [loop.id, loop.updatedAt, needsRead, onLoopChange, readDelayMs]);

  const save = async (next: LoopOutputContract | null) => {
    setSaving(true);
    try {
      const saved = await updateLoop(loop.id, { outputContract: next ? personContract(next) : null });
      onLoopChange(saved);
      setEditing(undefined);
      setReviewing(false);
      if (!next) {
        setAnalyzing(true);
        onLoopChange(await analyzeLoopOutputs(loop.id).finally(() => setAnalyzing(false)));
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not save outputs"));
    } finally {
      setSaving(false);
    }
  };

  const reread = async () => {
    setAnalyzing(true);
    try {
      onLoopChange(await analyzeLoopOutputs(loop.id, true));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not read the outputs"));
    } finally {
      setAnalyzing(false);
    }
  };

  const fallback = contract?.source === "fallback";
  return (
    <section className="loops-section" aria-label={t("What this loop will produce")}>
      <div className="loops-section-heading">
        <h3 className="loops-section-title">{t("What this loop will produce")}</h3>
        {!editing && !reviewing && hasInstructions && (
          <div className="loops-contract-actions">
            {contract?.source === "person" ? (
              <button className="loops-ghost-button is-small" type="button" disabled={saving} onClick={() => void save(null)}>
                <RotateCcw size={12} />
                {t("Use automatic outputs")}
              </button>
            ) : (
              contract && !unsaved && (
                <button className="loops-ghost-button is-small" type="button" disabled={analyzing} onClick={() => void reread()}>
                  {t("Read again")}
                </button>
              )
            )}
            <button className="loops-ghost-button is-small" type="button" disabled={analyzing} onClick={() => setEditing(contract ? { ...contract, outputs: contract.outputs.map((output) => ({ ...output, target: { ...output.target } })) } : { source: "person", readOnly: false, outputs: [blankOutput()] })}>
              {t("Edit outputs")}
            </button>
          </div>
        )}
      </div>
      <div className="loops-card is-contract">
        {fallback && !editing && (
          <p className="loops-contract-warning" role="alert">
            <AlertTriangle size={14} />
            <span>{t(CONTRACT_FALLBACK_NOTICE)}</span>
          </p>
        )}
        {contract?.source === "person" && contract.stale && !editing && <div
          className="loops-contract-stale is-neutral"
          role="status"
          tabIndex={suggestion?.source === "model" ? 0 : undefined}
          onClick={() => suggestion?.source === "model" && setReviewing(true)}
          onKeyDown={(event) => {
            if (suggestion?.source === "model" && (event.key === "Enter" || event.key === " ")) {
              event.preventDefault();
              setReviewing(true);
            }
          }}
        >
          <strong>{t("Instructions changed — review suggested outputs")}</strong>
          {suggestion?.source === "model" && !reviewing && <button type="button" className="loops-ghost-button is-small" onClick={(event) => { event.stopPropagation(); setReviewing(true); }}>{t("Review suggested outputs")}</button>}
          {suggestion?.source === "fallback" && <p className="loops-contract-hint">{t(CONTRACT_FALLBACK_NOTICE)}</p>}
          {!reviewing && <button type="button" className="loops-ghost-button is-small" disabled={saving} onClick={(event) => { event.stopPropagation(); void save(contract); }}>{t("Keep my outputs")}</button>}
        </div>}
        {unsaved && contract?.source !== "person" && <p className="loops-contract-hint">{t("Save the loop to read its outputs from the new instructions.")}</p>}
        {reviewing && contract && suggestion ? <LoopContractReview key={suggestion.instructionsHash} data={data} current={contract} suggested={suggestion} saving={saving} onSave={(next) => void save(next)} onCancel={() => setReviewing(false)} /> : editing ? (
          <div className="loops-contract-editor">
            <div className="loops-permission-row">
              <span>
                <strong>{t("Reply only")}</strong>
                <small>{t("The run reports in its reply and changes nothing in Flow or in connectors.")}</small>
              </span>
              <Toggle size="regular" checked={editing.readOnly} label={t("Reply only")} onChange={(checked) => setEditing({ ...editing, readOnly: checked })} />
            </div>
            <ul className="loops-contract-edit-list" aria-label={t("Outputs")}>
              {editing.outputs.map((output, index) => (
                <ContractOutputEditor
                  key={index}
                  output={output}
                  data={data}
                  loop={loop}
                  onChange={(next) => setEditing({ ...editing, outputs: editing.outputs.map((item, position) => (position === index ? next : item)) })}
                  onRemove={() => setEditing({ ...editing, outputs: editing.outputs.filter((_, position) => position !== index) })}
                />
              ))}
            </ul>
            <div className="loops-contract-edit-footer">
              <button className="loops-add-connector" type="button" onClick={() => setEditing({ ...editing, outputs: [...editing.outputs, blankOutput()] })}>
                <Plus size={14} />
                {t("Add output")}
              </button>
              <span className="loops-contract-edit-buttons">
                {!contractReady(editing) && <small className="loops-contract-hint">{t("Choose the document for each output that writes to an existing one.")}</small>}
                <button className="loops-ghost-button" type="button" onClick={() => setEditing(undefined)}>
                  {t("Cancel")}
                </button>
                <button className="loops-primary-button" type="button" disabled={saving || !contractReady(editing)} onClick={() => void save(editing)}>
                  {saving ? t("Saving…") : t("Save outputs")}
                </button>
              </span>
            </div>
          </div>
        ) : (analyzing && contract?.source !== "person") || (needsRead && !contract) ? (
          <p className="loops-contract-reading" role="status">{t("Reading the instructions…")}</p>
        ) : contract && !fallback ? (
          <>
            {!contract.readOnly &&
              contract.outputs.map((output, index) =>
                unchosenDocument(output) ? (
                  <div className="loops-contract-warning" role="alert" key={`unchosen-${index}`}>
                    <AlertTriangle size={14} />
                    <span>
                      {t("The instructions don't say which document to write to. Until you choose it, runs write to no existing document and need review.")}
                      {output.target.description && <q className="loops-contract-evidence" data-i18n-ignore>{output.target.description}</q>}
                    </span>
                    <ContractDocumentButton
                      output={output}
                      data={data}
                      loop={loop}
                      onPick={(document) =>
                        void save({ ...contract, outputs: contract.outputs.map((item, position) => (position === index ? { ...item, target: { ...item.target, documentId: document.id, title: document.title, mode: "existing", ambiguous: false } } : item)) })
                      }
                    />
                  </div>
                ) : null,
              )}
            <LoopContractSummary contract={contract} data={data} />
            {contract.source === "person" && <p className="loops-contract-source">{t("Set by a person")}</p>}
          </>
        ) : !hasInstructions ? (
          <p className="loops-contract-empty">{t("Write the instructions to see what the loop will produce.")}</p>
        ) : null}
      </div>
    </section>
  );
}
