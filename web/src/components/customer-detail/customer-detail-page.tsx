import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  Bell,
  Check,
  Copy,
  MoreHorizontal,
  Paperclip,
  Plus,
  Star,
  Trash2,
  UserRound,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { SelectControl } from "@/components/ui/select-control";
import { CustomerDialog } from "./customer-dialog";
import {
  addFavorite,
  addSubscription,
  createCustomerRequest,
  createIssue,
  deleteCustomer,
  deleteCustomerRequest,
  deleteCustomerRequestAttachment,
  removeFavorite,
  removeSubscription,
  updateCustomer,
  uploadCustomerRequestAttachment,
} from "@/lib/api";
import type { BootstrapData, Customer, CustomerRequest } from "@/types/flow";

import "./customer-detail-page.css";

export function CustomerDetailPage({
  data,
  customer,
  onBack,
  onReload,
  onOpenResource,
}: {
  data: BootstrapData;
  customer: Customer;
  onBack: () => void;
  onReload: () => Promise<void>;
  onOpenResource: (type: "issue" | "project", id: string) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const requests = data.customerRequests.filter(
    (item) => item.customerId === customer.id,
  );
  const favorite = data.favorites.some(
    (item) =>
      item.resourceType === "customer" && item.resourceId === customer.id,
  );
  const subscribed = data.subscriptions.some(
    (item) =>
      item.resourceType === "customer" && item.resourceId === customer.id,
  );
  useEffect(() => {
    const openRequest = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "r" || event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement || event.target instanceof HTMLElement && event.target.isContentEditable) return;
      event.preventDefault();
      setAdding(true);
    };
    addEventListener("keydown", openRequest);
    return () => removeEventListener("keydown", openRequest);
  }, []);
  return (
    <main className="main-panel customer-detail">
      <header className="customer-detail-header">
        <button onClick={onBack}>Customers</button>
        <span>›</span>
        <i>{customer.name.slice(0, 1).toUpperCase()}</i>
        <strong>{customer.name}</strong>
        <div>
          <button
            aria-label={favorite ? "Remove from favorites" : "Add to favorites"}
            onClick={() =>
              void (
                favorite
                  ? removeFavorite("customer", customer.id)
                  : addFavorite("customer", customer.id)
              ).then(onReload)
            }
          >
            <Star size={15} fill={favorite ? "currentColor" : "none"} />
          </button>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button aria-label="Open customer menu">
                <MoreHorizontal size={16} />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content
                className="customer-detail-menu"
                align="end"
              >
                <DropdownMenu.Item
                  onSelect={() => navigator.clipboard.writeText(location.href)}
                >
                  <Copy size={14} />
                  Copy link
                </DropdownMenu.Item>
                <DropdownMenu.Item
                  onSelect={() =>
                    void (
                      subscribed
                        ? removeSubscription("customer", customer.id)
                        : addSubscription("customer", customer.id)
                    ).then(onReload)
                  }
                >
                  <Bell size={14} />
                  {subscribed ? "Unsubscribe" : "Subscribe"}
                </DropdownMenu.Item>
                <DropdownMenu.Separator />
                <DropdownMenu.Item
                  className="danger"
                  onSelect={() => setDeleteOpen(true)}
                >
                  <Trash2 size={14} />
                  Delete
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
          <button aria-label="Copy page URL" onClick={() => void navigator.clipboard.writeText(location.href)}><Copy size={15}/></button>
          <button
            aria-label={
              subscribed ? "Unsubscribe" : "Setup customer notifications"
            }
            onClick={() =>
              void (
                subscribed
                  ? removeSubscription("customer", customer.id)
                  : addSubscription("customer", customer.id)
              ).then(onReload)
            }
          >
            <Bell size={15} fill={subscribed ? "currentColor" : "none"} />
          </button>
        </div>
      </header>
      <div className="customer-detail-body">
        <section className="customer-hero">
          <div className="customer-logo">
            {customer.name.slice(0, 1).toUpperCase()}
          </div>
          <div>
            <h1>{customer.name}</h1>
            {customer.domains.length > 0 && (
              <small>{customer.domains.join(", ")}</small>
            )}
          </div>
          <button onClick={() => setEditOpen(true)}>Edit customer</button>
        </section>
        <section className="customer-properties">
          <label>Status</label>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button>
                <span
                  className="customer-status-dot"
                  style={{
                    background: data.customerStatuses.find(
                      (value) =>
                        value.name.toLowerCase() ===
                        customer.status.toLowerCase(),
                    )?.color,
                  }}
                />
                {customer.status}
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content
                className="customer-detail-menu"
                align="start"
              >
                {data.customerStatuses
                  .filter((value) => !value.archivedAt)
                  .map((value) => (
                    <DropdownMenu.Item
                      key={value.id}
                      onSelect={() =>
                        void updateCustomer(customer.id, {
                          status: value.name,
                        }).then(onReload)
                      }
                    >
                      <span
                        className="customer-status-dot"
                        style={{ background: value.color }}
                      />
                      {value.name}
                      {customer.status.toLowerCase() ===
                        value.name.toLowerCase() && <Check size={13} />}
                    </DropdownMenu.Item>
                  ))}
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
          {customer.tier && <><label>Tier</label>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button>
                <span
                  className="customer-status-dot"
                  style={{
                    background: data.customerTiers.find(
                      (value) => value.name === customer.tier,
                    )?.color,
                  }}
                />
                {customer.tier || "No tier"}
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content
                className="customer-detail-menu"
                align="start"
              >
                <DropdownMenu.Item
                  onSelect={() =>
                    void updateCustomer(customer.id, { tier: "" }).then(
                      onReload,
                    )
                  }
                >
                  No tier{!customer.tier && <Check size={13} />}
                </DropdownMenu.Item>
                {data.customerTiers
                  .filter((value) => !value.archivedAt)
                  .map((value) => (
                    <DropdownMenu.Item
                      key={value.id}
                      onSelect={() =>
                        void updateCustomer(customer.id, {
                          tier: value.name,
                        }).then(onReload)
                      }
                    >
                      <span
                        className="customer-status-dot"
                        style={{ background: value.color }}
                      />
                      {value.name}
                      {customer.tier === value.name && <Check size={13} />}
                    </DropdownMenu.Item>
                  ))}
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root></>}
          {customer.size != null && <><label>Size</label><span>{customer.size}</span></>}
          {customer.annualRevenue != null && <><label>Annual revenue</label><span>{new Intl.NumberFormat(undefined,{style:"currency",currency:"USD",notation:"compact"}).format(customer.annualRevenue)}</span></>}
          <label>Requests</label><span>{requests.length}</span>
          {customer.ownerId && (
            <>
              <label>Owner</label>
              <span>
                <UserRound size={13} />
                {
                  data.users.find((user) => user.id === customer.ownerId)
                    ?.displayName
                }
              </span>
            </>
          )}
        </section>
        <div className="customer-requests-heading">
          <span>Requests</span>
          <small>{requests.length}</small>
          <button onClick={() => setAdding(true)}>
            <Plus size={13} />
            Add request
          </button>
        </div>
        {adding && (
          <CustomerRequestComposer
            data={data}
            customer={customer}
            onCancel={() => setAdding(false)}
            onCreated={async () => {
              setAdding(false);
              await onReload();
            }}
          />
        )}
        {requests.length ? (
          <div className="customer-request-list">
            {requests.map((request) => (
              <CustomerRequestRow
                data={data}
                key={request.id}
                request={request}
                onOpenResource={onOpenResource}
                onReload={onReload}
              />
            ))}
          </div>
        ) : (
          !adding && (
            <div className="customer-requests-empty">
              <strong>Customer requests</strong>
              <p>
                No customer requests created yet. Use a supported integration to
                automatically create requests, or create one manually.
              </p>
              <button onClick={() => setAdding(true)}>
                <Plus size={13} />
                Add request <kbd>Ctrl R</kbd>
              </button>
            </div>
          )
        )}
      </div>
      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="customer-delete-dialog">
          <DialogTitle>Delete {customer.name}</DialogTitle>
          <p>Are you sure you want to delete this customer?</p>
          <footer>
            <button onClick={() => setDeleteOpen(false)}>Cancel</button>
            <button
              className="danger"
              onClick={() => void deleteCustomer(customer.id).then(onBack)}
            >
              Delete
            </button>
          </footer>
        </DialogContent>
      </Dialog>
      <CustomerDialog
        open={editOpen}
        users={data.users}
        customer={customer}
        onOpenChange={setEditOpen}
        onSubmit={async (input) => {
          try {
            await updateCustomer(customer.id, input);
            await onReload();
          } catch (error) {
            toast.error(
              error instanceof Error
                ? error.message
                : "Could not update customer",
            );
            throw error;
          }
        }}
      />
    </main>
  );
}

function CustomerRequestRow({
  data,
  request,
  onOpenResource,
  onReload,
}: {
  data: BootstrapData;
  request: CustomerRequest;
  onOpenResource: (type: "issue" | "project", id: string) => void;
  onReload: () => Promise<void>;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const issue = data.issues.find((item) => item.id === request.issueId);
  const project = data.projects.find((item) => item.id === request.projectId);
  const upload = async (file?: File) => {
    if (!file) return;
    try {
      await uploadCustomerRequestAttachment(request.id, file);
      await onReload();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not attach file",
      );
    } finally {
      if (fileRef.current) fileRef.current.value = "";
    }
  };
  return (
    <div className="customer-request-row">
      <div>
        <strong>{request.body}</strong>
        <small>
          {request.creator.displayName} ·{" "}
          {new Date(request.createdAt).toLocaleDateString()} · {request.source}
        </small>
        {issue && (
          <button onClick={() => onOpenResource("issue", issue.id)}>
            {issue.identifier} {issue.title}
          </button>
        )}
        {project && (
          <button onClick={() => onOpenResource("project", project.id)}>
            {project.name}
          </button>
        )}
        {request.attachments.length > 0 && (
          <div className="customer-request-attachments">
            {request.attachments.map((attachment) => (
              <span key={attachment.id}>
                <a href={attachment.url} target="_blank" rel="noreferrer">
                  <Paperclip size={12} />
                  {attachment.title}
                </a>
                <button
                  aria-label={`Delete ${attachment.title}`}
                  onClick={() =>
                    void deleteCustomerRequestAttachment(
                      request.id,
                      attachment.id,
                    ).then(onReload)
                  }
                >
                  <Trash2 size={11} />
                </button>
              </span>
            ))}
          </div>
        )}
      </div>
      <input
        ref={fileRef}
        hidden
        type="file"
        onChange={(event) => void upload(event.target.files?.[0])}
      />
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button aria-label="Request actions">
            <MoreHorizontal size={14} />
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content className="customer-detail-menu" align="end">
            <DropdownMenu.Item onSelect={() => fileRef.current?.click()}>
              <Paperclip size={14} />
              Attach file
            </DropdownMenu.Item>
            <DropdownMenu.Separator />
            <DropdownMenu.Item
              className="danger"
              onSelect={() =>
                void deleteCustomerRequest(request.id).then(onReload)
              }
            >
              <Trash2 size={14} />
              Delete request
            </DropdownMenu.Item>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </div>
  );
}

function CustomerRequestComposer({
  data,
  customer,
  onCancel,
  onCreated,
}: {
  data: BootstrapData;
  customer: Customer;
  onCancel: () => void;
  onCreated: () => Promise<void>;
}) {
  const [body, setBody] = useState("");
  const [target, setTarget] = useState("new");
  const [saving, setSaving] = useState(false);
  const [files, setFiles] = useState<File[]>([]);
  const fileRef = useRef<HTMLInputElement>(null);
  const submit = async () => {
    setSaving(true);
    try {
      let issueId: string | undefined;
      let projectId: string | undefined;
      if (target === "new") {
        const issue = await createIssue({
          title: `Customer request from ${customer.name}`,
          description: body,
          teamId: data.teams[0]?.id ?? "",
        });
        issueId = issue.id;
      } else if (target.startsWith("issue:")) issueId = target.slice(6);
      else if (target.startsWith("project:")) projectId = target.slice(8);
      const request = await createCustomerRequest({
        customerId: customer.id,
        body,
        source: "manual",
        issueId,
        projectId,
      });
      await Promise.all(
        files.map((file) => uploadCustomerRequestAttachment(request.id, file)),
      );
      await onCreated();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not add request",
      );
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="customer-request-composer">
      <textarea
        autoFocus
        aria-label="Note"
        placeholder="Add request details"
        value={body}
        onChange={(e) => setBody(e.target.value)}
      />
      {files.length > 0 && (
        <div className="customer-request-file-queue">
          {files.map((file, index) => (
            <span key={`${file.name}:${index}`}>
              <Paperclip size={11} />
              {file.name}
              <button
                aria-label={`Remove ${file.name}`}
                onClick={() =>
                  setFiles((current) =>
                    current.filter((_, itemIndex) => itemIndex !== index),
                  )
                }
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="customer-request-tools">
        <button
          title="Attach images, files, or videos"
          onClick={() => fileRef.current?.click()}
        >
          <Paperclip size={14} />
        </button>
        <input
          ref={fileRef}
          hidden
          multiple
          type="file"
          onChange={(event) => {
            setFiles((current) => [
              ...current,
              ...Array.from(event.target.files ?? []),
            ]);
            event.target.value = "";
          }}
        />
        <button onClick={onCancel}>Cancel</button>
        <button
          className="primary"
          disabled={!body.trim() || saving}
          onClick={() => void submit()}
        >
          {saving ? "Adding…" : "Add request"}
        </button>
      </div>
      <label>
        This request will be added to
        <SelectControl label="Request target" value={target} onChange={setTarget} options={[
          { value: "new", label: `New issue · Customer request from ${customer.name}` },
          ...data.issues.slice(0, 30).map(issue => ({ value: `issue:${issue.id}`, label: `${issue.identifier} ${issue.title}`, groupLabel: "Existing issues", entityName: true })),
          ...data.projects.map(project => ({ value: `project:${project.id}`, label: project.name, groupLabel: "Projects", entityName: true })),
        ]}/>
      </label>
    </div>
  );
}
