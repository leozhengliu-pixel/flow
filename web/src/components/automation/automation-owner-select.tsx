/**
 * LS-0091 AutomationOwnerSelect — Avatar row + change-owner action.
 */
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { ChevronDown } from "lucide-react";
import { PeopleMenuItems } from "@/components/property/people-menu-items";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import {
  automationOwnerAccess,
  getChangeAutomationOwnerAction,
  resolveEffectiveOwner,
} from "@/lib/automation-owner";
import type { User } from "@/types/flow";
import "./automation-trust.css";

type Appearance = "property" | "inline";

type Props = {
  ownerId?: string | null;
  creatorId?: string | null;
  creator?: User | null;
  users: User[];
  viewer?: User | null;
  viewerRole?: string | null;
  appearance?: Appearance;
  disabled?: boolean;
  onChangeOwner: (ownerId: string) => void;
};

export function AutomationOwnerSelect({
  ownerId,
  creatorId,
  creator,
  users,
  viewer,
  viewerRole,
  appearance = "property",
  disabled,
  onChangeOwner,
}: Props) {
  const { t } = useI18n();
  const owner = resolveEffectiveOwner({
    ownerId,
    creatorId,
    creator,
    users,
    viewer,
  });
  const access = automationOwnerAccess(viewerRole);
  const action = getChangeAutomationOwnerAction(access);
  const canChange = action.allowed && !disabled;
  const people = users.filter((user) => user.active && !user.app);

  const trigger = (
    <button
      type="button"
      className={
        appearance === "inline"
          ? "automation-owner-inline"
          : "automation-owner-property"
      }
      aria-label={`${t("Owner")}: ${owner.displayName}`}
      title={canChange ? t("Change owner") : action.reason}
      disabled={!canChange}
      data-testid="automation-owner-select"
    >
      <UserAvatar
        className="automation-owner-avatar"
        avatarUrl={owner.avatarUrl}
        name={owner.displayName}
      />
      <span data-i18n-ignore className="automation-owner-name">
        {owner.displayName}
      </span>
      {canChange && <ChevronDown size={14} aria-hidden />}
    </button>
  );

  if (!canChange) {
    return (
      <div className="automation-owner-row">
        {appearance === "property" && (
          <span className="automation-owner-label">{t("Owner")}</span>
        )}
        {trigger}
      </div>
    );
  }

  return (
    <div className="automation-owner-row">
      {appearance === "property" && (
        <span className="automation-owner-label">{t("Owner")}</span>
      )}
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            data-flow-motion="floating"
            className="automation-owner-menu"
            align="start"
            sideOffset={4}
          >
            <PeopleMenuItems
              users={people.length ? people : users}
              selectedId={owner.id}
              onSelect={onChangeOwner}
              itemClassName="automation-owner-menu-item"
            />
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </div>
  );
}
