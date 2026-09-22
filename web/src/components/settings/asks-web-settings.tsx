import { useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  Copy,
  ExternalLink,
  Globe,
  Plus,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { useI18n } from "@/i18n/i18n";
import {
  createAsksWebPage,
  createAsksWebSettings,
  deleteAsksWebPage,
  deleteAsksWebSettings,
  updateAsksWebPage,
  updateAsksWebSettings,
  verifyAsksWebDNS,
} from "@/lib/api";
import type { AsksWebSettings, BootstrapData } from "@/types/flow";

import "./asks-settings.css";
import "./asks-web-settings.css";

/** Constant page limit — billing entitlements are out of scope. */
export const MAX_ASKS_WEB_PAGES_PER_CONFIGURATION = 10;

export type AsksWebStatusKind =
  | "configured"
  | "pending"
  | "incomplete"
  | "held";

export function resolveAsksWebStatus(
  settings: AsksWebSettings,
): { kind: AsksWebStatusKind; message: string } {
  if (settings.customDomainStatus === "blocked") {
    return { kind: "held", message: "Domain activation blocked" };
  }
  if (
    settings.customDomainStatus === "pending" ||
    (settings.hostingStatus === "pending" && !settings.dnsVerified)
  ) {
    if (settings.customDomainStatus === "pending") {
      return {
        kind: "pending",
        message: "Custom domain activation in progress",
      };
    }
    return { kind: "pending", message: "Hosting not configured" };
  }
  if (settings.hostingStatus !== "configured" || !settings.dnsVerified) {
    return { kind: "incomplete", message: "Hosting not configured" };
  }
  if (!settings.emailDomainConfigured) {
    return { kind: "incomplete", message: "Email domain not configured" };
  }
  if (!settings.samlConfigured) {
    // SAML is deferred stub — configured hosting/email is enough for green.
  }
  return { kind: "configured", message: "Configured" };
}

function message(error: unknown) {
  return error instanceof Error ? error.message : "Could not update Asks web forms";
}

export function AsksWebStatus({
  settings,
  canManage = true,
  onAddPage,
  onOpenUrl,
  onEdit,
  onAddCustomDomain,
  onDelete,
}: {
  settings: AsksWebSettings;
  canManage?: boolean;
  onAddPage?: () => void;
  onOpenUrl?: () => void;
  onEdit?: () => void;
  onAddCustomDomain?: () => void;
  onDelete?: () => void;
}) {
  const { t } = useI18n();
  const status = resolveAsksWebStatus(settings);
  const pageCount = settings.pages?.length ?? 0;
  const atLimit = pageCount >= MAX_ASKS_WEB_PAGES_PER_CONFIGURATION;

  return (
    <div className="asks-web-status" data-status={status.kind}>
      <div className="asks-web-status-main">
        <span className="asks-web-status-dot" aria-hidden />
        <div>
          <strong data-i18n-ignore>{settings.title || settings.hostname}</strong>
          <small>{t(status.message)}</small>
          {!settings.samlConfigured && status.kind === "configured" && (
            <small className="asks-web-status-note">
              {t("SAML not configured")}
            </small>
          )}
        </div>
      </div>
      <div className="asks-web-status-actions">
        <button
          type="button"
          className="asks-web-chip"
          disabled={!canManage || atLimit || !onAddPage}
          onClick={onAddPage}
        >
          <Plus size={14} />
          {t("Add page")}
          {atLimit ? ` (${pageCount}/${MAX_ASKS_WEB_PAGES_PER_CONFIGURATION})` : ""}
        </button>
        <button
          type="button"
          className="asks-web-chip"
          disabled={!settings.asksUrl || !onOpenUrl}
          onClick={onOpenUrl}
        >
          <ExternalLink size={14} />
          {t("Open URL")}
        </button>
        <button
          type="button"
          className="asks-web-chip"
          disabled={!canManage || !onEdit}
          onClick={onEdit}
        >
          {t("Edit configuration")}
        </button>
        {!settings.customDomain && (
          <button
            type="button"
            className="asks-web-chip"
            disabled={!canManage || !onAddCustomDomain}
            onClick={onAddCustomDomain}
          >
            {t("Add custom domain")}
          </button>
        )}
        <button
          type="button"
          className="asks-web-chip danger"
          disabled={!canManage || !onDelete}
          onClick={onDelete}
        >
          <Trash2 size={14} />
          {t("Delete configuration")}
        </button>
      </div>
    </div>
  );
}

type WizardStep = "host" | "email" | "dns" | "saml";

export function AsksWebSettingsWizardPage({
  data,
  settingsId,
  onBack,
  onComplete,
  onReload,
}: {
  data: BootstrapData;
  settingsId?: string;
  onBack: () => void;
  onComplete: (id: string) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const existing = (data.asksWebSettings ?? []).find(
    (item) => item.id === settingsId,
  );
  const [step, setStep] = useState<WizardStep>("host");
  const [title, setTitle] = useState(existing?.title ?? "");
  const [hostname, setHostname] = useState(existing?.hostname ?? "");
  const [email, setEmail] = useState(existing?.emailAddress ?? "");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{
    settings: AsksWebSettings;
    dnsRecord: { type: string; name: string; value: string };
  }>();

  const steps: { id: WizardStep; label: string }[] = [
    { id: "host", label: "Configure Asks web" },
    { id: "email", label: "Email notifications" },
    { id: "dns", label: "DNS" },
    { id: "saml", label: "SAML" },
  ];

  const createOrUpdateHost = async () => {
    if (!hostname.trim()) return;
    setBusy(true);
    try {
      if (existing || created) {
        const id = existing?.id ?? created!.settings.id;
        const settings = await updateAsksWebSettings(id, {
          title: title.trim() || hostname.trim(),
          hostname: hostname.trim(),
        });
        setCreated({
          settings,
          dnsRecord:
            created?.dnsRecord ?? {
              type: "TXT",
              name: `_flow-asks.${settings.hostname}`,
              value: "",
            },
        });
      } else {
        const result = await createAsksWebSettings({
          title: title.trim() || hostname.trim(),
          hostname: hostname.trim(),
          emailAddress: email.trim() || undefined,
        });
        setCreated(result);
      }
      setStep("email");
      await onReload();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };

  const saveEmail = async () => {
    const id = existing?.id ?? created?.settings.id;
    if (!id) return;
    setBusy(true);
    try {
      const settings = await updateAsksWebSettings(id, {
        emailAddress: email.trim(),
      });
      setCreated((prev) =>
        prev
          ? { ...prev, settings }
          : {
              settings,
              dnsRecord: {
                type: "TXT",
                name: `_flow-asks.${settings.hostname}`,
                value: "",
              },
            },
      );
      setStep("dns");
      await onReload();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };

  const verify = async () => {
    const id = existing?.id ?? created?.settings.id;
    if (!id) return;
    setBusy(true);
    try {
      const settings = await verifyAsksWebDNS(id, created?.dnsRecord.value);
      setCreated((prev) =>
        prev
          ? { ...prev, settings }
          : {
              settings,
              dnsRecord: {
                type: "TXT",
                name: `_flow-asks.${settings.hostname}`,
                value: "",
              },
            },
      );
      toast.success(t("Domain verified"));
      setStep("saml");
      await onReload();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };

  const finish = async () => {
    const id = existing?.id ?? created?.settings.id;
    if (!id) return;
    toast.success(t("Asks Web Forms configuration complete"));
    await onReload();
    onComplete(id);
  };

  return (
    <div className="asks-wizard asks-web-wizard">
      <div className="feature-settings">
        <header className="feature-header">
          <h1>{t(existing ? "Edit Asks web forms" : "Add Asks web forms")}</h1>
          <p>
            {t(
              "Host a public Asks form on your domain with email notifications and DNS verification.",
            )}
          </p>
        </header>
        <button type="button" className="asks-settings-back" onClick={onBack}>
          <ChevronLeft size={14} />
          {t("Back to Asks")}
        </button>
        <div className="asks-wizard-steps" aria-label={t("Wizard steps")}>
          {steps.map((item, index) => {
            const active = item.id === step;
            const done = steps.findIndex((value) => value.id === step) > index;
            return (
              <span key={item.id} data-active={active} data-done={done}>
                {index + 1}. {t(item.label)}
              </span>
            );
          })}
        </div>
        <div className="asks-wizard-panel">
          {step === "host" && (
            <>
              <header>
                <h2>{t("Configure Asks web")}</h2>
                <p>{t("Choose a hostname and title for your Asks portal.")}</p>
              </header>
              <label>
                {t("Title")}
                <input
                  aria-label={t("Title")}
                  value={title}
                  onChange={(event) => setTitle(event.target.value)}
                  placeholder="Acme Asks"
                />
              </label>
              <label>
                {t("Hostname")}
                <input
                  aria-label={t("Hostname")}
                  value={hostname}
                  onChange={(event) =>
                    setHostname(
                      event.target.value
                        .toLowerCase()
                        .replace(/[^a-z0-9.-]/g, ""),
                    )
                  }
                  placeholder="asks.acme.com"
                />
              </label>
              <div className="asks-wizard-actions">
                <button type="button" className="feature-button" onClick={onBack}>
                  {t("Cancel")}
                </button>
                <button
                  type="button"
                  className="feature-button primary"
                  disabled={busy || !hostname.trim()}
                  onClick={() => void createOrUpdateHost()}
                >
                  {t("Continue")}
                </button>
              </div>
            </>
          )}
          {step === "email" && (
            <>
              <header>
                <h2>{t("Configure email notifications")}</h2>
                <p>
                  {t(
                    "Optional address used for Asks notifications and replies.",
                  )}
                </p>
              </header>
              <label>
                {t("Notification email")}
                <input
                  aria-label={t("Notification email")}
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  placeholder="asks@acme.com"
                />
              </label>
              <div className="asks-wizard-actions">
                <button
                  type="button"
                  className="feature-button"
                  onClick={() => setStep("host")}
                >
                  {t("Back")}
                </button>
                <button
                  type="button"
                  className="feature-button primary"
                  disabled={busy}
                  onClick={() => void saveEmail()}
                >
                  {t("Continue")}
                </button>
              </div>
            </>
          )}
          {step === "dns" && (
            <>
              <header>
                <h2>{t("Set up DNS entries")}</h2>
                <p>
                  {t(
                    "Add this TXT record at your DNS provider, then verify the domain.",
                  )}
                </p>
              </header>
              <dl className="asks-wizard-dns">
                <dt>{t("Type")}</dt>
                <dd data-i18n-ignore>{created?.dnsRecord.type ?? "TXT"}</dd>
                <dt>{t("Name")}</dt>
                <dd data-i18n-ignore>
                  {created?.dnsRecord.name ??
                    `_flow-asks.${hostname || "asks.example.com"}`}
                </dd>
                <dt>{t("Value")}</dt>
                <dd data-i18n-ignore>
                  {created?.dnsRecord.value ||
                    t("Verification value issued on create")}
                </dd>
              </dl>
              <div className="asks-wizard-actions">
                <button
                  type="button"
                  className="feature-button"
                  onClick={() => setStep("email")}
                >
                  {t("Back")}
                </button>
                <button
                  type="button"
                  className="feature-button primary"
                  disabled={busy}
                  onClick={() => void verify()}
                >
                  {t("Verify domain")}
                </button>
              </div>
            </>
          )}
          {step === "saml" && (
            <>
              <header>
                <h2>{t("Configure SAML")}</h2>
                <p>
                  {t(
                    "SAML single sign-on for Asks web forms can be configured later.",
                  )}
                </p>
              </header>
              <div className="asks-coming-soon">
                <strong>{t("Coming soon")}</strong>
                <span>
                  {t(
                    "SAML configuration for Asks web forms is not available yet.",
                  )}
                </span>
              </div>
              <div className="asks-wizard-actions">
                <button
                  type="button"
                  className="feature-button"
                  onClick={() => setStep("dns")}
                >
                  {t("Back")}
                </button>
                <button
                  type="button"
                  className="feature-button primary"
                  disabled={busy}
                  onClick={() => void finish()}
                >
                  {t("Finish")}
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

export function AsksWebSettingsPage({
  data,
  settingsId,
  onBack,
  onReload,
  onOpenWizard,
  onOpenPage,
  onAddPage,
}: {
  data: BootstrapData;
  settingsId: string;
  onBack: () => void;
  onReload: () => Promise<void>;
  onOpenWizard: (id: string) => void;
  onOpenPage: (settingsId: string, pageId: string) => void;
  onAddPage: (settingsId: string) => void;
}) {
  const { t } = useI18n();
  const settings = (data.asksWebSettings ?? []).find(
    (item) => item.id === settingsId,
  );
  const [busy, setBusy] = useState(false);
  const [customDomain, setCustomDomain] = useState(
    settings?.customDomain ?? "",
  );
  const canManage = ["admin", "owner"].includes(data.viewerRole);

  if (!settings) {
    return (
      <div className="feature-settings">
        <header className="feature-header">
          <h1>{t("Asks web forms")}</h1>
          <p>{t("Configuration not found")}</p>
        </header>
        <button type="button" className="asks-settings-back" onClick={onBack}>
          <ChevronLeft size={14} />
          {t("Back to Asks")}
        </button>
      </div>
    );
  }

  const copyUrl = async () => {
    try {
      await navigator.clipboard.writeText(settings.asksUrl);
      toast.success(t("Copied Asks URL"));
    } catch {
      toast.error(t("Could not copy"));
    }
  };

  const saveCustomDomain = async () => {
    if (!canManage || !customDomain.trim()) return;
    setBusy(true);
    try {
      await updateAsksWebSettings(settings.id, {
        customDomain: customDomain.trim(),
      });
      toast.success(t("Custom domain saved"));
      await onReload();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!canManage) return;
    if (!window.confirm(t("Delete this Asks web forms configuration?"))) return;
    setBusy(true);
    try {
      await deleteAsksWebSettings(settings.id);
      toast.success(t("Configuration deleted"));
      await onReload();
      onBack();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="feature-settings asks-web-settings-page">
      <header className="feature-header">
        <h1>{t("Asks web forms")}</h1>
        <p>{t("Custom domain, email sending, and pages for your public Asks portal.")}</p>
      </header>
      <button type="button" className="asks-settings-back" onClick={onBack}>
        <ChevronLeft size={14} />
        {t("Back to Asks")}
      </button>

      <AsksWebStatus
        settings={settings}
        canManage={canManage && !busy}
        onAddPage={() => onAddPage(settings.id)}
        onOpenUrl={() => window.open(settings.asksUrl, "_blank", "noopener")}
        onEdit={() => onOpenWizard(settings.id)}
        onAddCustomDomain={() => {
          const el = document.getElementById("asks-web-custom-domain");
          el?.scrollIntoView({ behavior: "smooth", block: "center" });
          (el as HTMLInputElement | null)?.focus();
        }}
        onDelete={() => void remove()}
      />

      <section className="feature-section">
        <header>
          <h2>{t("Asks URL")}</h2>
          <p>{t("Share this link so people can submit Asks on the web.")}</p>
        </header>
        <div className="feature-card">
          <div className="feature-row">
            <div>
              <strong data-i18n-ignore>{settings.asksUrl}</strong>
            </div>
            <button type="button" className="feature-button" onClick={() => void copyUrl()}>
              <Copy size={14} />
              {t("Copy")}
            </button>
          </div>
        </div>
      </section>

      <section className="feature-section" id="asks-web-custom-domain-section">
        <header>
          <h2>{t("Custom domain")}</h2>
          <p>{t("Point a domain you own at this Asks configuration.")}</p>
        </header>
        <div className="feature-card asks-web-form-card">
          <label>
            {t("Domain")}
            <input
              id="asks-web-custom-domain"
              aria-label={t("Custom domain")}
              value={customDomain}
              disabled={!canManage || busy}
              onChange={(event) => setCustomDomain(event.target.value)}
              placeholder="asks.acme.com"
            />
          </label>
          <div className="asks-wizard-actions">
            <button
              type="button"
              className="feature-button primary"
              disabled={!canManage || busy || !customDomain.trim()}
              onClick={() => void saveCustomDomain()}
            >
              {settings.customDomainStatus === "pending"
                ? t("Retry activation")
                : t("Save domain")}
            </button>
          </div>
          {settings.customDomainStatus !== "none" && (
            <p className="asks-web-inline-status">
              {t("Status")}: {t(settings.customDomainStatus)}
            </p>
          )}
        </div>
      </section>

      <section className="feature-section">
        <header>
          <h2>{t("Email")}</h2>
          <p>{t("Notification and reply-from address for this configuration.")}</p>
        </header>
        <div className="feature-card">
          <div className="feature-row">
            <div>
              <strong>
                {settings.emailAddress || t("No email address configured")}
              </strong>
              <span>
                {settings.emailDomainConfigured
                  ? t("Email domain configured")
                  : t("Email domain not configured")}
              </span>
            </div>
          </div>
        </div>
      </section>

      <section className="feature-section">
        <header>
          <h2>{t("SAML")}</h2>
          <p>{t("Optional single sign-on for your Asks portal.")}</p>
        </header>
        <div className="asks-coming-soon">
          <strong>{t("Coming soon")}</strong>
          <span>
            {t("SAML configuration for Asks web forms is not available yet.")}
          </span>
        </div>
      </section>

      <section className="feature-section">
        <header>
          <h2>{t("Pages")}</h2>
          <p>{t("Public form pages under this configuration.")}</p>
        </header>
        {(settings.pages?.length ?? 0) === 0 ? (
          <div className="asks-coming-soon">
            <strong>{t("No pages yet")}</strong>
            <span>{t("Add a page to publish a public Asks form.")}</span>
          </div>
        ) : (
          <div className="feature-card">
            {settings.pages.map((page) => (
              <button
                key={page.id}
                type="button"
                className="asks-clickable-row"
                onClick={() => onOpenPage(settings.id, page.id)}
              >
                <div className="feature-row">
                  <span className="feature-row-icon">
                    <Globe size={18} />
                  </span>
                  <div>
                    <strong data-i18n-ignore>{page.title}</strong>
                    <span data-i18n-ignore>/{page.slug}</span>
                  </div>
                  <ChevronRight size={15} />
                </div>
              </button>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

export function AsksWebPageSettingsPage({
  data,
  settingsId,
  pageId,
  onBack,
  onReload,
}: {
  data: BootstrapData;
  settingsId: string;
  pageId?: string;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const settings = (data.asksWebSettings ?? []).find(
    (item) => item.id === settingsId,
  );
  const existing = settings?.pages.find((page) => page.id === pageId);
  const isNew = !pageId || pageId === "new";
  const templates = data.issueTemplates ?? [];
  const canManage = ["admin", "owner"].includes(data.viewerRole);

  const [slug, setSlug] = useState(existing?.slug ?? "");
  const [title, setTitle] = useState(existing?.title ?? "");
  const [templateIds, setTemplateIds] = useState<string[]>(
    existing?.templateIds ?? [],
  );
  const [autoReplyCreated, setAutoReplyCreated] = useState(
    existing?.autoReplyCreated ?? "",
  );
  const [autoReplyCompleted, setAutoReplyCompleted] = useState(
    existing?.autoReplyCompleted ?? "",
  );
  const [autoReplyCanceled, setAutoReplyCanceled] = useState(
    existing?.autoReplyCanceled ?? "",
  );
  const [emailReplies, setEmailReplies] = useState(
    existing?.emailRepliesEnabled ?? false,
  );
  const [busy, setBusy] = useState(false);

  const atLimit =
    isNew &&
    (settings?.pages.length ?? 0) >= MAX_ASKS_WEB_PAGES_PER_CONFIGURATION;

  const save = async () => {
    if (!canManage || !settings || atLimit) return;
    if (!slug.trim() || !title.trim()) {
      toast.error(t("Slug and title are required"));
      return;
    }
    setBusy(true);
    try {
      if (isNew) {
        await createAsksWebPage(settings.id, {
          slug: slug.trim(),
          title: title.trim(),
          templateIds,
          autoReplyCreated: autoReplyCreated.trim() || undefined,
          autoReplyCompleted: autoReplyCompleted.trim() || undefined,
          autoReplyCanceled: autoReplyCanceled.trim() || undefined,
          emailRepliesEnabled: emailReplies,
        });
        toast.success(t("Page created"));
      } else if (existing) {
        await updateAsksWebPage(settings.id, existing.id, {
          slug: slug.trim(),
          title: title.trim(),
          templateIds,
          autoReplyCreated: autoReplyCreated.trim(),
          autoReplyCompleted: autoReplyCompleted.trim(),
          autoReplyCanceled: autoReplyCanceled.trim(),
          emailRepliesEnabled: emailReplies,
        });
        toast.success(t("Page saved"));
      }
      await onReload();
      onBack();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!canManage || !settings || !existing) return;
    if (!window.confirm(t("Delete this Asks web page?"))) return;
    setBusy(true);
    try {
      await deleteAsksWebPage(settings.id, existing.id);
      toast.success(t("Page deleted"));
      await onReload();
      onBack();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };

  const toggleTemplate = (id: string) => {
    setTemplateIds((current) =>
      current.includes(id)
        ? current.filter((value) => value !== id)
        : [...current, id],
    );
  };

  const moveTemplate = (id: string, direction: -1 | 1) => {
    setTemplateIds((current) => {
      const index = current.indexOf(id);
      if (index < 0) return current;
      const next = index + direction;
      if (next < 0 || next >= current.length) return current;
      const copy = [...current];
      const [item] = copy.splice(index, 1);
      copy.splice(next, 0, item);
      return copy;
    });
  };

  if (!settings) {
    return (
      <div className="feature-settings">
        <header className="feature-header">
          <h1>{t("Asks web page")}</h1>
          <p>{t("Configuration not found")}</p>
        </header>
        <button type="button" className="asks-settings-back" onClick={onBack}>
          <ChevronLeft size={14} />
          {t("Back")}
        </button>
      </div>
    );
  }

  return (
    <div className="feature-settings asks-web-page-settings">
      <header className="feature-header">
        <h1>{t(isNew ? "New Asks web page" : "Asks web page")}</h1>
        <p>{t("Slug, templates, and auto-reply messages for this form page.")}</p>
      </header>
      <button type="button" className="asks-settings-back" onClick={onBack}>
        <ChevronLeft size={14} />
        {t("Back to configuration")}
      </button>

      {atLimit && (
        <div className="asks-coming-soon">
          <strong>{t("Page limit reached")}</strong>
          <span>
            {t("This configuration already has the maximum number of pages.")}{" "}
            ({MAX_ASKS_WEB_PAGES_PER_CONFIGURATION})
          </span>
        </div>
      )}

      <section className="feature-section">
        <header>
          <h2>{t("Page")}</h2>
        </header>
        <div className="feature-card asks-web-form-card">
          <label>
            {t("Title")}
            <input
              aria-label={t("Page title")}
              value={title}
              disabled={!canManage || busy || atLimit}
              onChange={(event) => setTitle(event.target.value)}
            />
          </label>
          <label>
            {t("Slug")}
            <input
              aria-label={t("Page slug")}
              value={slug}
              disabled={!canManage || busy || atLimit}
              onChange={(event) =>
                setSlug(
                  event.target.value
                    .toLowerCase()
                    .replace(/[^a-z0-9-]/g, ""),
                )
              }
              placeholder="support"
            />
          </label>
        </div>
      </section>

      <section className="feature-section">
        <header>
          <h2>{t("Templates")}</h2>
          <p>{t("Issue templates offered on this page.")}</p>
        </header>
        <div className="feature-card">
          {templates.length === 0 ? (
            <div className="feature-row">
              <span>{t("No templates")}</span>
            </div>
          ) : (
            templates.map((template) => {
              const selected = templateIds.includes(template.id);
              const order = templateIds.indexOf(template.id);
              return (
                <div className="asks-web-template-row" key={template.id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={selected}
                      disabled={!canManage || busy || atLimit}
                      onChange={() => toggleTemplate(template.id)}
                    />
                    <span data-i18n-ignore>{template.name}</span>
                  </label>
                  {selected && (
                    <span className="asks-web-template-order">
                      <button
                        type="button"
                        disabled={order === 0 || busy}
                        aria-label={t("Move template up")}
                        onClick={() => moveTemplate(template.id, -1)}
                      >
                        ↑
                      </button>
                      <button
                        type="button"
                        disabled={order === templateIds.length - 1 || busy}
                        aria-label={t("Move template down")}
                        onClick={() => moveTemplate(template.id, 1)}
                      >
                        ↓
                      </button>
                    </span>
                  )}
                </div>
              );
            })
          )}
        </div>
      </section>

      <section className="feature-section">
        <header>
          <h2>{t("Auto-reply")}</h2>
          <p>{t("Optional messages sent when an Ask is created, completed, or canceled.")}</p>
        </header>
        <div className="feature-card asks-web-form-card">
          <label>
            {t("Created")}
            <textarea
              aria-label={t("Auto-reply created")}
              value={autoReplyCreated}
              disabled={!canManage || busy || atLimit}
              onChange={(event) => setAutoReplyCreated(event.target.value)}
              rows={3}
            />
          </label>
          <label>
            {t("Completed")}
            <textarea
              aria-label={t("Auto-reply completed")}
              value={autoReplyCompleted}
              disabled={!canManage || busy || atLimit}
              onChange={(event) => setAutoReplyCompleted(event.target.value)}
              rows={3}
            />
          </label>
          <label>
            {t("Canceled")}
            <textarea
              aria-label={t("Auto-reply canceled")}
              value={autoReplyCanceled}
              disabled={!canManage || busy || atLimit}
              onChange={(event) => setAutoReplyCanceled(event.target.value)}
              rows={3}
            />
          </label>
        </div>
      </section>

      <section className="feature-section">
        <header>
          <h2>{t("Email replies")}</h2>
        </header>
        <div className="feature-card">
          <label className="asks-web-toggle-row">
            <input
              type="checkbox"
              checked={emailReplies}
              disabled={!canManage || busy || atLimit}
              onChange={(event) => setEmailReplies(event.target.checked)}
            />
            <span>{t("Allow email replies for this page")}</span>
          </label>
        </div>
      </section>

      <div className="asks-wizard-actions asks-web-page-actions">
        {!isNew && (
          <button
            type="button"
            className="feature-button danger"
            disabled={!canManage || busy}
            onClick={() => void remove()}
          >
            {t("Delete page")}
          </button>
        )}
        <button type="button" className="feature-button" onClick={onBack}>
          {t("Cancel")}
        </button>
        <button
          type="button"
          className="feature-button primary"
          disabled={!canManage || busy || atLimit}
          onClick={() => void save()}
        >
          {isNew ? t("Create page") : t("Save")}
        </button>
      </div>
    </div>
  );
}

export function AsksWebFormsSection({
  data,
  canManage,
  onOpenWizard,
  onOpenSettings,
  onAddPage,
  onReload,
}: {
  data: BootstrapData;
  canManage: boolean;
  onOpenWizard: (id?: string) => void;
  onOpenSettings: (id: string) => void;
  onAddPage: (settingsId: string) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const configurations = data.asksWebSettings ?? [];
  const [deleting, setDeleting] = useState<string>();

  const remove = async (id: string) => {
    if (!canManage) return;
    if (!window.confirm(t("Delete this Asks web forms configuration?"))) return;
    setDeleting(id);
    try {
      await deleteAsksWebSettings(id);
      toast.success(t("Configuration deleted"));
      await onReload();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setDeleting(undefined);
    }
  };

  if (configurations.length === 0) {
    return (
      <div className="asks-coming-soon asks-web-empty">
        <strong>{t("No Asks web configuration")}</strong>
        <span>
          {t("Add Asks Web forms to collect requests on your own domain.")}
        </span>
        <button
          type="button"
          className="feature-button primary"
          disabled={!canManage}
          onClick={() => onOpenWizard()}
        >
          <Plus size={14} />
          {t("Add Asks Web forms")}
        </button>
      </div>
    );
  }

  return (
    <div className="asks-web-list">
      {configurations.map((settings) => (
        <div key={settings.id} className="feature-card asks-web-list-card">
          <AsksWebStatus
            settings={settings}
            canManage={canManage && deleting !== settings.id}
            onAddPage={() => onAddPage(settings.id)}
            onOpenUrl={() =>
              window.open(settings.asksUrl, "_blank", "noopener")
            }
            onEdit={() => onOpenSettings(settings.id)}
            onAddCustomDomain={() => onOpenSettings(settings.id)}
            onDelete={() => void remove(settings.id)}
          />
          <button
            type="button"
            className="asks-clickable-row"
            onClick={() => onOpenSettings(settings.id)}
          >
            <div className="feature-row">
              <div>
                <strong data-i18n-ignore>{settings.hostname}</strong>
                <span>
                  {(settings.pages?.length ?? 0) === 1
                    ? t("1 page")
                    : t("{n} pages").replace(
                        "{n}",
                        String(settings.pages?.length ?? 0),
                      )}
                </span>
              </div>
              <ChevronRight size={15} />
            </div>
          </button>
        </div>
      ))}
      <button
        type="button"
        className="feature-button"
        disabled={!canManage}
        onClick={() => onOpenWizard()}
      >
        <Plus size={14} />
        {t("Add Asks Web forms")}
      </button>
    </div>
  );
}
