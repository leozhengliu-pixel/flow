import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Check, ChevronDown, ChevronLeft, ChevronUp, Copy, Mail, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";

import { AppLink } from "@/components/ui/app-link";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { SelectControl } from "@/components/ui/select-control";
import { ScopedFlowTooltip } from "@/components/ui/tooltip";
import { useI18n } from "@/i18n/i18n";
import {
  createEmailIntakeAddress,
  deleteEmailIntakeAddress,
  listEmailIntakeAddresses,
  updateEmailIntakeAddress,
  updateWorkspacePreferences,
  verifyEmailIntakeAddress,
} from "@/lib/api";
import { settingsPath } from "@/lib/app-routes";
import type { BootstrapData, EmailIntakeAddress, EmailIntakeDnsRecord } from "@/types/flow";
import { intakeDescription, intakeTitle } from "./email-intake-format";
import { SettingsToggle } from "./settings-primitives";

import "./feature-settings.css";
import "./asks-email-intake.css";

const NO_TEMPLATE = "";
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

type StepState = "completed" | "inProgress" | "notStarted";
const stepState = (index: number, current: number): StepState =>
  index < current ? "completed" : index === current ? "inProgress" : "notStarted";

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

function emailDomain(address?: string) {
  return address?.split("@")[1]?.toLowerCase() ?? "";
}

function emailIntakePath(data: BootstrapData, id: string, edit = false) {
  return `${settingsPath(data.workspace.urlKey, "asks")}/email-intake/${encodeURIComponent(id)}${edit ? "/edit" : ""}`;
}

/** The team's standard templates; Linear doesn't offer form or workspace templates for email. */
function teamTemplates(data: BootstrapData, teamId: string) {
  return (data.issueTemplates ?? []).filter((item) => item.teamId === teamId && item.templateType !== "customForm");
}

/** Without a public intake host in dev, fall back to the server's default sender domain. */
function outboundFrom(address?: EmailIntakeAddress) {
  return address?.outboundFromEmail || "issues@flow.app";
}

function copyText(text: string, success: string) {
  void navigator.clipboard?.writeText(text).then(() => toast.info(success));
}

function BackLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <AppLink className="asks-intake-back" href={href}>
      <ChevronLeft size={14} />
      <span data-i18n-ignore>{children}</span>
    </AppLink>
  );
}

function PageHeader({ title, description, back, actions }: { title: ReactNode; description?: ReactNode; back: ReactNode; actions?: ReactNode }) {
  return (
    <>
      {back}
      <header className="feature-header asks-intake-header">
        <div>
          <h1>{title}</h1>
          {description && <p>{description}</p>}
        </div>
        {actions}
      </header>
    </>
  );
}

function WizardStep({ title, description, state, line = "faded", children }: { title: ReactNode; description: ReactNode; state: StepState; line?: "solid" | "faded"; children: ReactNode }) {
  const open = state !== "notStarted";
  return (
    <section className="asks-intake-step" data-state={state} aria-current={state === "inProgress" ? "step" : undefined}>
      <div className="asks-intake-step-rail" aria-hidden="true">
        <span className="asks-intake-step-dot">{state === "completed" ? <Check size={10} strokeWidth={3} /> : <i />}</span>
        <span className="asks-intake-step-line" data-faded={line === "faded" && state !== "completed"} />
      </div>
      <div className="asks-intake-step-body">
        <header>
          <h2>{title}</h2>
          {open && <p>{description}</p>}
        </header>
        {open && <div className="asks-intake-step-content">{children}</div>}
      </div>
    </section>
  );
}

function Card({ children }: { children: ReactNode }) {
  return <div className="asks-intake-card">{children}</div>;
}

function Row({ title, description, error, htmlFor, disabled, children }: { title: ReactNode; description?: ReactNode; error?: string; htmlFor?: string; disabled?: boolean; children?: ReactNode }) {
  return (
    <div className="asks-intake-row" data-disabled={disabled || undefined}>
      <div className="asks-intake-row-copy">
        {htmlFor ? <label htmlFor={htmlFor}>{title}</label> : <strong>{title}</strong>}
        {error ? <span className="asks-intake-error" role="alert">{error}</span> : description && <span>{description}</span>}
      </div>
      {children && <div className="asks-intake-row-control">{children}</div>}
    </div>
  );
}

function TeamAndTemplateRows({ data, teamId, templateId, teamError, onTeam, onTemplate }: { data: BootstrapData; teamId: string; templateId: string; teamError?: string; onTeam: (id: string) => void; onTemplate: (id: string) => void }) {
  const { t } = useI18n();
  const teams = data.teams.filter((team) => !team.retiredAt);
  const templates = teamTemplates(data, teamId);
  const template = data.issueTemplates?.find((item) => item.id === templateId);
  const warningTeam = template?.teamId && template.teamId !== teamId ? data.teams.find((team) => team.id === template.teamId) : undefined;
  return (
    <>
      <Row title={t("Team")} error={teamError}>
        <SelectControl
          className="asks-intake-select"
          label={t("Team")}
          value={teamId}
          options={[...(teamId ? [] : [{ value: "", label: t("Select a team"), disabled: true }]), ...teams.map((team) => ({ value: team.id, label: team.name, entityName: true }))]}
          onChange={onTeam}
        />
      </Row>
      <Row
        title={t("Apply template")}
        disabled={!teamId}
        description={warningTeam ? <span className="asks-intake-warning">{t("Template creates issues in the {team} team").split("{team}")[0]}<strong data-i18n-ignore>{warningTeam.name}</strong>{t("Template creates issues in the {team} team").split("{team}")[1]}</span> : t("Optionally use a template to fill issue properties")}
      >
        <SelectControl
          className="asks-intake-select"
          disabled={!teamId}
          label={t("Apply template")}
          value={templateId}
          options={[{ value: NO_TEMPLATE, label: t("No template") }, ...templates.map((item) => ({ value: item.id, label: item.name, entityName: true }))]}
          onChange={onTemplate}
        />
      </Row>
    </>
  );
}

function ContinueButton({ busy, onClick, children }: { busy: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <div className="asks-intake-continue">
      <button type="button" className="asks-intake-primary" disabled={busy} onClick={onClick}>{children}</button>
    </div>
  );
}

/** DNS records to authenticate the custom address's domain for outbound replies. */
function DnsRecordsTable({ records, wizard = false, onVerify }: { records: EmailIntakeDnsRecord[]; wizard?: boolean; onVerify: (record: EmailIntakeDnsRecord) => Promise<void> }) {
  const { t } = useI18n();
  return (
    <div className="asks-intake-dns" role="table" aria-label={t("DNS records")} data-wizard={wizard || undefined}>
      <div className="asks-intake-dns-row is-header" role="row">
        <span role="columnheader">{t("Type")}</span>
        <span role="columnheader">{t("Name")}</span>
        <span role="columnheader">{t("Content")}</span>
        <span role="columnheader">{t("Status")}</span>
        <span role="columnheader" />
      </div>
      {records.length === 0 ? (
        <div className="asks-intake-dns-empty">{t(wizard ? "Generating" : "Loading")}</div>
      ) : (
        records.map((record) => (
          <div className="asks-intake-dns-row" role="row" key={`${record.type}-${record.name}-${record.content}`}>
            <span role="cell" data-i18n-ignore>{record.type}</span>
            <CopyCell value={record.name} />
            <CopyCell value={record.content} />
            <span role="cell" className="asks-intake-dns-status" data-verified={record.isVerified}><i />{t(record.isVerified ? "Verified" : "Unverified")}</span>
            <span role="cell">
              {!record.isVerified && (
                <button type="button" className="asks-intake-borderless" onClick={() => void onVerify(record)}>{t("Verify")}</button>
              )}
            </span>
          </div>
        ))
      )}
    </div>
  );
}

function CopyCell({ value }: { value: string }) {
  const { t } = useI18n();
  return (
    <span role="cell" className="asks-intake-dns-copy">
      <span data-i18n-ignore title={value}>{value}</span>
      <ScopedFlowTooltip label={t("Copy to clipboard")}>
        <button type="button" aria-label={t("Copy to clipboard")} onClick={() => copyText(value, t("Copied to clipboard"))}><Copy size={12} /></button>
      </ScopedFlowTooltip>
    </span>
  );
}

function useVerifyRecord(address: EmailIntakeAddress | undefined, onVerified: (address: EmailIntakeAddress) => void) {
  const { t } = useI18n();
  return async (record: EmailIntakeDnsRecord) => {
    if (!address) return;
    toast.info(t("Verifying DNS records"), { description: t("DNS record changes could take up to 72 hours to propagate. Please check back later") });
    try {
      onVerified(await verifyEmailIntakeAddress(address.teamId, address.id, record.content));
    } catch {
      // Records stay unverified until DNS propagates; Linear only refreshes the status.
    }
  };
}

/**
 * Linear's "Add email intake" / "Edit email intake" wizard
 * (Settings › Asks › Email › +): connect a team and template, configure the
 * sender and the custom address that forwards to the generated intake
 * address, then optionally authenticate the custom domain for replies.
 */
export function NewAsksEmailIntakePage({
  data,
  addressId,
  onBack,
  onReload,
  onOpenAddress,
}: {
  data: BootstrapData;
  /** Edits an existing address (`/email-intake/:id/edit`). */
  addressId?: string;
  onBack: () => void;
  onReload: () => Promise<void>;
  onOpenAddress?: (addressId: string) => void;
}) {
  const { t } = useI18n();
  const editing = Boolean(addressId);
  const existing = (data.emailIntakeAddresses ?? []).find((item) => item.id === addressId);
  const [step, setStep] = useState(editing ? 1 : 0);
  const [address, setAddress] = useState<EmailIntakeAddress | undefined>(existing);
  const [teamId, setTeamId] = useState(existing?.teamId ?? "");
  const [templateId, setTemplateId] = useState(existing?.templateId ?? NO_TEMPLATE);
  const [senderName, setSenderName] = useState(existing?.senderName ?? "");
  const [forwarding, setForwarding] = useState(existing?.forwardingEmailAddress ?? "");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const verifyRecord = useVerifyRecord(address, setAddress);

  // Bootstrap omits DNS records and the default sender, so load the API view.
  useEffect(() => {
    if (!existing) return;
    let active = true;
    void listEmailIntakeAddresses(existing.teamId).then((items) => {
      const found = items.find((item) => item.id === existing.id);
      if (active && found) setAddress(found);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [existing]);

  const validate = (current: number) => {
    const next: Record<string, string> = {};
    if (!teamId) next.teamId = t("Please select a team");
    if (current >= 1) {
      if (!senderName.trim()) next.senderName = t("Please enter a name");
      if (forwarding.trim() && !EMAIL_PATTERN.test(forwarding.trim())) next.forwardingEmailAddress = t("Please enter a valid email address");
    }
    setErrors(next);
    return Object.keys(next).length === 0;
  };

  const submit = async () => {
    if (busy || !validate(step)) return;
    setBusy(true);
    try {
      if (!address) {
        const created = await createEmailIntakeAddress(teamId, { type: "asks", templateId: templateId || undefined });
        setAddress(created.address);
        await onReload();
        setStep(1);
        return;
      }
      const updated = await updateEmailIntakeAddress(address.teamId, address.id, {
        teamId,
        templateId,
        ...(step >= 1 ? { senderName: senderName.trim(), forwardingEmailAddress: forwarding.trim() } : {}),
      });
      setAddress(updated);
      await onReload();
      if (step < 2) setStep(step + 1);
      else if (onOpenAddress) onOpenAddress(updated.id);
      else onBack();
    } catch (error) {
      const text = errorMessage(error, t("Could not save email intake"));
      if (/already in use/i.test(text)) setErrors({ forwardingEmailAddress: t("Email address already in use") });
      else if (/valid email/i.test(text)) setErrors({ forwardingEmailAddress: t("Please enter a valid email address") });
      else toast.error(text);
    } finally {
      setBusy(false);
    }
  };

  const forwardingSaved = address?.forwardingEmailAddress;
  const back = editing && address
    ? <BackLink href={emailIntakePath(data, address.id)}>{intakeTitle(address)}</BackLink>
    : <BackLink href={settingsPath(data.workspace.urlKey, "asks")}>{t("Asks")}</BackLink>;

  return (
    <div className="feature-settings asks-intake-page">
      <PageHeader
        back={back}
        title={t(editing ? "Edit email intake" : "Add email intake")}
        description={<>{t("Create issues by emailing a custom email address.")} <a className="asks-intake-docs" href="https://linear.app/docs/linear-asks-email" target="_blank" rel="noreferrer">{t("Docs")} ↗</a></>}
      />
      <div className="asks-intake-wizard">
        {!editing && (
          <WizardStep title={t("Connect to Flow team")} description={t("Each intake email is connected to a single Flow team")} line="solid" state={stepState(0, step)}>
            <Card>
              <TeamAndTemplateRows
                data={data}
                teamId={teamId}
                templateId={templateId}
                teamError={errors.teamId}
                onTeam={(id) => { setTeamId(id); setTemplateId(NO_TEMPLATE); setErrors({}); }}
                onTemplate={setTemplateId}
              />
            </Card>
            {step === 0 && <ContinueButton busy={busy} onClick={() => void submit()}>{t("Continue")}</ContinueButton>}
          </WizardStep>
        )}
        <WizardStep
          title={t(editing ? "Edit email forwarding" : "Configure email address")}
          description={<>{t(editing ? "Edit your custom address, then in your email provider’s settings, configure it to forward emails to the intake address below." : "Specify your custom address, then in your email provider’s settings, configure it to forward emails to the intake address below.")} <a className="asks-intake-docs" href="https://linear.app/docs/linear-asks-email" target="_blank" rel="noreferrer">{t("Docs")} ↗</a></>}
          line="solid"
          state={stepState(1, step)}
        >
          <Card>
            <Row title={t("Intake address")}>
              <ScopedFlowTooltip label={t("Copy to clipboard")}>
                <button type="button" className="asks-intake-address" onClick={() => address && copyText(address.address, t("Email copied to clipboard"))}>
                  <span data-i18n-ignore>{address?.address}</span>
                  <Copy size={14} aria-label={t("Copy to clipboard")} />
                </button>
              </ScopedFlowTooltip>
            </Row>
            <Row title={t("Sender name")} htmlFor="intake-sender-name" error={errors.senderName}>
              <input id="intake-sender-name" className="asks-intake-input" autoComplete="off" placeholder={t("e.g. Helpdesk")} value={senderName} onChange={(event) => setSenderName(event.target.value)} />
            </Row>
            <Row title={<>{t("Custom address")} <span className="asks-intake-optional">{t("(optional)")}</span></>} htmlFor="intake-forwarding" error={errors.forwardingEmailAddress}>
              <input id="intake-forwarding" className="asks-intake-input" autoComplete="off" placeholder={t("e.g. helpdesk@acme.com")} value={forwarding} onChange={(event) => { setForwarding(event.target.value); if (step > 1) setStep(1); }} />
            </Row>
          </Card>
          {step === 1 && <ContinueButton busy={busy} onClick={() => void submit()}>{t("Continue")}</ContinueButton>}
        </WizardStep>
        <WizardStep
          title={<>{t("Configure email domain")} <span className="asks-intake-optional">{t("(optional)")}</span></>}
          description={forwardingSaved
            ? <>{t("By default, outgoing emails from Flow are sent from")} <strong data-i18n-ignore>{outboundFrom(address)}</strong>{t(". To use your own email domain, access the DNS settings for")} <strong data-i18n-ignore>{emailDomain(forwardingSaved)}</strong>{t(", and add the following DNS records to authenticate your domain.")}</>
            : <>{t("Add a custom address above if you’d like to send email responses from your own email domain, otherwise, outgoing emails will be sent from")} <strong data-i18n-ignore>{outboundFrom(address)}</strong></>}
          state={stepState(2, step)}
        >
          {forwardingSaved && <Card><DnsRecordsTable wizard records={address?.dnsRecords ?? []} onVerify={verifyRecord} /></Card>}
          {step === 2 && <ContinueButton busy={busy} onClick={() => void submit()}>{t("Done")}</ContinueButton>}
        </WizardStep>
      </div>
    </div>
  );
}

function OutboundStatus({ data, address, onVerified }: { data: BootstrapData; address: EmailIntakeAddress; onVerified: (address: EmailIntakeAddress) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const verifyRecord = useVerifyRecord(address, onVerified);
  const fromDomain = emailDomain(outboundFrom(address));
  if (!address.forwardingEmailAddress)
    return (
      <div className="asks-intake-outbound">
        <div className="asks-intake-outbound-header is-expanded">
          <div>
            <strong>{t("Outbound sending status")}</strong>
            <span className="asks-intake-outbound-state"><i data-status="active" /><span className="is-active">{t("Sending from")} <span data-i18n-ignore>{fromDomain}</span></span></span>
          </div>
          <AppLink className="asks-intake-borderless" href={emailIntakePath(data, address.id, true)}>{t("Add address")}</AppLink>
        </div>
        <p className="asks-intake-outbound-note">{t("Provide a custom address if you’d like to send email responses from your own email domain, otherwise, outgoing emails will be sent from")} <strong data-i18n-ignore>{outboundFrom(address)}</strong></p>
      </div>
    );
  const records = address.dnsRecords ?? [];
  const verified = records.length > 0 && records.every((record) => record.isVerified);
  const status = records.length === 0 ? "loading" : verified ? "active" : "incomplete";
  const domain = emailDomain(address.forwardingEmailAddress);
  return (
    <div className="asks-intake-outbound">
      <div className={`asks-intake-outbound-header${open ? " is-expanded" : ""}`}>
        <div>
          <strong>{t("Outbound sending status")}</strong>
          <span className="asks-intake-outbound-state">
            <i data-status={status} />
            {status === "active" && <span><span className="is-active">{t("DNS Active")}</span> - {t("sending from")} <span data-i18n-ignore>{domain}</span></span>}
            {status === "incomplete" && <span><span className="is-incomplete">{t("DNS incomplete")}</span> - {t("sending from")} <span data-i18n-ignore>{fromDomain}</span></span>}
            {status === "loading" && <span>{t("DNS loading…")}</span>}
          </span>
        </div>
        <button type="button" className="asks-intake-borderless" aria-expanded={open} onClick={() => setOpen(!open)}>
          {t(open ? "Hide" : "Details")}{open ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
        </button>
      </div>
      {open && (
        <div className="asks-intake-outbound-details">
          <p className="asks-intake-outbound-note">{t("By default, outgoing emails from Flow are sent from")} <strong data-i18n-ignore>{outboundFrom(address)}</strong>{t(". To use your own email domain, access the DNS settings for")} <strong data-i18n-ignore>{domain}</strong>{t(", and add the following DNS records to authenticate your domain.")}</p>
          <DnsRecordsTable records={records} onVerify={verifyRecord} />
        </div>
      )}
    </div>
  );
}

/** Linear's Asks email intake settings page (Settings › Asks › an email address). */
export function AsksEmailIntakeDetailPage({
  data,
  addressId,
  onBack,
  onReload,
}: {
  data: BootstrapData;
  addressId: string;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const bootstrapAddress = (data.emailIntakeAddresses ?? []).find((item) => item.id === addressId);
  const [loaded, setLoaded] = useState<EmailIntakeAddress>();
  const [busy, setBusy] = useState(false);
  const address = useMemo(() => (bootstrapAddress ? { ...bootstrapAddress, ...(loaded?.id === bootstrapAddress.id ? { dnsRecords: loaded.dnsRecords, outboundFromEmail: loaded.outboundFromEmail, forwardingDomainVerifiedAt: loaded.forwardingDomainVerifiedAt } : {}) } : undefined), [bootstrapAddress, loaded]);
  const admin = ["admin", "owner"].includes(data.viewerRole);
  const customersEnabled = data.workspaceSettings.featureFlags["customer-requests"] !== false;
  const asksEmails = data.workspaceSettings.featureSettings?.asksEmailAddresses ?? [];

  useEffect(() => {
    if (!bootstrapAddress) return;
    let active = true;
    void listEmailIntakeAddresses(bootstrapAddress.teamId).then((items) => {
      const found = items.find((item) => item.id === bootstrapAddress.id);
      if (active && found) setLoaded(found);
    }).catch(() => undefined);
    return () => { active = false; };
  }, [bootstrapAddress]);

  const update = async (input: Parameters<typeof updateEmailIntakeAddress>[2]) => {
    if (!address) return;
    setBusy(true);
    try {
      setLoaded(await updateEmailIntakeAddress(address.teamId, address.id, input));
      await onReload();
    } catch (error) {
      toast.error(errorMessage(error, t("Could not save email intake")));
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!address) return;
    const description = intakeDescription(address);
    const confirmed = await confirmAction(t("Delete {name}?").replace("{name}", description ? `${intakeTitle(address)} (${description})` : intakeTitle(address)), { description: t("You cannot undo this action."), confirmLabel: t("Delete"), danger: true });
    if (!confirmed) return;
    try {
      if (asksEmails.includes(address.address))
        await updateWorkspacePreferences({ featureSettings: { asksEmailAddresses: asksEmails.filter((value) => value !== address.address) } }, data.workspace.urlKey);
      await deleteEmailIntakeAddress(address.teamId, address.id);
      await onReload();
      onBack();
      toast.success(t("Email intake deleted"));
    } catch (error) {
      toast.error(errorMessage(error, t("Could not delete email intake")));
    }
  };

  const back = <BackLink href={settingsPath(data.workspace.urlKey, "asks")}>{t("Asks")}</BackLink>;
  if (!address)
    return (
      <div className="feature-settings asks-intake-page">
        <PageHeader back={back} title={t("Email address not found")} description={t("This address may have been removed")} />
      </div>
    );
  const title = intakeTitle(address);
  const description = intakeDescription(address);
  return (
    <div className="feature-settings asks-intake-page">
      <PageHeader
        back={back}
        title={
          <ScopedFlowTooltip label={t("Copy to clipboard")}>
            <button type="button" className="asks-intake-copy-title" onClick={() => copyText(title, t("Copied to clipboard"))}><span data-i18n-ignore>{title}</span></button>
          </ScopedFlowTooltip>
        }
        description={description && <span className="asks-intake-title-description"><span data-i18n-ignore>{description}</span>{address.address !== description && <span className="asks-intake-muted" data-i18n-ignore>({address.address})</span>}</span>}
        actions={admin && (
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild><button type="button" className="asks-intake-menu-trigger" aria-label={t("Open menu")}><MoreHorizontal size={16} /></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content data-flow-motion="floating" className="asks-intake-menu" align="end" sideOffset={4}>
                <DropdownMenu.Item asChild><AppLink href={emailIntakePath(data, address.id, true)}><Pencil size={14} /><span>{t("Edit")}</span></AppLink></DropdownMenu.Item>
                <DropdownMenu.Item onSelect={() => copyText(address.address, t("Intake address copied to clipboard"))}><Mail size={14} /><span>{t("Copy address")}</span></DropdownMenu.Item>
                <DropdownMenu.Separator />
                <DropdownMenu.Item onSelect={() => void remove()}><Trash2 size={14} /><span>{t("Delete")}</span></DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        )}
      />
      <section className="asks-intake-section">
        <div className="asks-intake-card">
          {address.system ? (
            <OutboundStatus data={data} address={address} onVerified={setLoaded} />
          ) : (
            <Row title={t("Status")} description={t(address.verificationState === "verified" ? "Verified and receiving email" : "Waiting for DNS verification")} />
          )}
        </div>
        <Card>
          <TeamAndTemplateRows
            data={data}
            teamId={address.teamId}
            templateId={address.templateId ?? NO_TEMPLATE}
            onTeam={(id) => void update({ teamId: id })}
            onTemplate={(id) => void update({ templateId: id })}
          />
        </Card>
      </section>
      <section className="asks-intake-section">
        <header>
          <h2>{t("Customer requests")}</h2>
          <p>{t("Automatically link inbound emails with customers based on the sender’s email domain")}</p>
        </header>
        <Card>
          <Row title={t("Link incoming emails as customer requests")} description={customersEnabled ? undefined : t("Customer requests are not enabled for this workspace")}>
            <SettingsToggle
              checked={customersEnabled && Boolean(address.customerRequestsEnabled)}
              disabled={busy || !admin || !customersEnabled}
              label={t("Link incoming emails as customer requests")}
              onChange={(value) => void update({ customerRequestsEnabled: value })}
            />
          </Row>
        </Card>
      </section>
    </div>
  );
}
