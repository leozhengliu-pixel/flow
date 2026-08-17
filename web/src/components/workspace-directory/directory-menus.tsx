import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import * as Popover from "@radix-ui/react-popover";
import {
  ArrowDownNarrowWide,
  Check,
  ChevronDown,
  ChevronRight,
  Search,
} from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";

export interface DirectoryFilterChoice {
  id: string;
  label: string;
  meta?: string;
  icon?: ReactNode;
}

export interface DirectoryFilterGroup {
  id: string;
  label: string;
  icon: ReactNode;
  choices?: DirectoryFilterChoice[];
  separatorBefore?: boolean;
}

export function DirectoryFilterMenu({
  groups,
  selected,
  trigger = "icon",
  onAdvanced,
  onChoice,
  onDirect,
}: {
  groups: DirectoryFilterGroup[];
  selected: Record<string, Set<string>>;
  trigger?: "icon" | "add";
  onAdvanced: () => void;
  onChoice: (groupId: string, choiceId: string, checked: boolean) => void;
  onDirect: (groupId: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [activeGroup, setActiveGroup] = useState<string | null>(null);
  const filteredGroups = useMemo(
    () =>
      groups.filter((group) =>
        group.label.toLowerCase().includes(query.trim().toLowerCase()),
      ),
    [groups, query],
  );
  return (
    <DropdownMenu.Root
      onOpenChange={(open) => {
        if (!open) {
          setQuery("");
          setActiveGroup(null);
        }
      }}
    >
      <DropdownMenu.Trigger asChild>
        {trigger === "icon" ? (
          <button
            aria-label="Add filter"
            className="workspace-directory__icon-button"
            type="button"
          >
            <FlowFilterIcon />
          </button>
        ) : (
          <button
            aria-label="Add another filter"
            className="workspace-filter-bar__add"
            type="button"
          >
            +
          </button>
        )}
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          className="workspace-directory-filter-menu"
          sideOffset={3.5}
        >
          <label className="workspace-directory-filter-menu__search">
            <Search />
            <input
              aria-label="Add Filter…"
              autoFocus
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => event.stopPropagation()}
              placeholder="Add Filter…"
              value={query}
            />
            <kbd>F</kbd>
          </label>
          <div className="workspace-directory-filter-menu__items">
            {!query && (
              <>
                <DropdownMenu.Item
                  className="workspace-directory-filter-menu__advanced"
                  onSelect={onAdvanced}
                  onPointerMove={() => setActiveGroup(null)}
                >
                  <FlowAdvancedFilterIcon />
                  <span>Advanced filter</span>
                </DropdownMenu.Item>
                <DropdownMenu.Separator />
              </>
            )}
            {filteredGroups.map((group) => (
              <FilterGroup
                group={group}
                key={group.id}
                open={activeGroup === group.id}
                onOpen={() => setActiveGroup(group.id)}
                onPointerAway={() => setActiveGroup(null)}
                onChoice={onChoice}
                onDirect={onDirect}
                selected={selected[group.id] ?? new Set()}
              />
            ))}
            {query && filteredGroups.length === 0 && (
              <div className="workspace-directory-filter-menu__none">
                No filters found
              </div>
            )}
          </div>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function FilterGroup({
  group,
  open,
  onOpen,
  onPointerAway,
  selected,
  onChoice,
  onDirect,
}: {
  group: DirectoryFilterGroup;
  open: boolean;
  onOpen: () => void;
  onPointerAway: () => void;
  selected: Set<string>;
  onChoice: (groupId: string, choiceId: string, checked: boolean) => void;
  onDirect: (groupId: string) => void;
}) {
  const [query, setQuery] = useState("");
  const choices = (group.choices ?? []).filter((choice) =>
    choice.label.toLowerCase().includes(query.trim().toLowerCase()),
  );
  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);
  return (
    <>
      {group.separatorBefore && <DropdownMenu.Separator />}
      {group.choices ? (
        <DropdownMenu.Sub
          open={open}
          onOpenChange={(nextOpen) => nextOpen && onOpen()}
        >
          <DropdownMenu.SubTrigger className="workspace-directory-filter-menu__item">
            {group.icon}
            <span>{group.label}</span>
            <ChevronRight />
          </DropdownMenu.SubTrigger>
          <DropdownMenu.Portal>
            <DropdownMenu.SubContent
              className="workspace-directory-filter-submenu"
              sideOffset={5}
            >
              <label className="workspace-directory-filter-menu__search is-submenu">
                <Search />
                <input
                  aria-label="Filter…"
                  onChange={(event) => setQuery(event.target.value)}
                  onKeyDown={(event) => event.stopPropagation()}
                  placeholder="Filter…"
                  value={query}
                />
              </label>
              <div className="workspace-directory-filter-menu__items">
                {choices.map((choice) => (
                  <DropdownMenu.CheckboxItem
                    checked={selected.has(choice.id)}
                    className="workspace-directory-filter-menu__choice"
                    key={choice.id}
                    onCheckedChange={(checked) =>
                      onChoice(group.id, choice.id, checked === true)
                    }
                    onSelect={(event) => event.preventDefault()}
                  >
                    <span className="workspace-directory-filter-menu__checkbox">
                      {selected.has(choice.id) && <Check />}
                    </span>
                    {choice.icon}
                    <span className="workspace-directory-filter-menu__choice-label">
                      {choice.label}
                    </span>
                    {choice.meta && <small>{choice.meta}</small>}
                  </DropdownMenu.CheckboxItem>
                ))}
              </div>
            </DropdownMenu.SubContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Sub>
      ) : (
        <DropdownMenu.Item
          className="workspace-directory-filter-menu__item"
          onSelect={() => onDirect(group.id)}
          onPointerMove={onPointerAway}
        >
          {group.icon}
          <span>{group.label}</span>
        </DropdownMenu.Item>
      )}
    </>
  );
}

export interface DirectoryPropertyOption<T extends string> {
  id: T;
  label: string;
}

export interface DirectoryOrderingOption<T extends string> {
  id: T;
  label: string;
}

export function DirectoryDisplayMenu<
  TProperty extends string,
  TOrdering extends string,
>({
  ordering,
  orderingOptions,
  descending,
  properties,
  propertyOptions,
  onOrdering,
  onDirection,
  onProperty,
}: {
  ordering: TOrdering;
  orderingOptions: DirectoryOrderingOption<TOrdering>[];
  descending: boolean;
  properties: Set<TProperty>;
  propertyOptions: DirectoryPropertyOption<TProperty>[];
  onOrdering: (ordering: TOrdering) => void;
  onDirection: () => void;
  onProperty: (property: TProperty) => void;
}) {
  const activeOrdering =
    orderingOptions.find((option) => option.id === ordering)?.label ?? ordering;
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button
          aria-label="Display options"
          className="workspace-directory__icon-button"
          type="button"
        >
          <FlowDisplayIcon />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          align="end"
          className="workspace-directory-display-menu"
          sideOffset={3.5}
        >
          <div className="workspace-directory-display-menu__ordering">
            <span>Ordering</span>
            <button
              aria-label="Direction"
              className="workspace-directory-display-menu__direction"
              data-descending={descending}
              onClick={onDirection}
              type="button"
            >
              <ArrowDownNarrowWide />
            </button>
            <DropdownMenu.Root>
              <DropdownMenu.Trigger asChild>
                <button
                  aria-label="Choose ordering"
                  className="workspace-directory-display-menu__select"
                  type="button"
                >
                  {activeOrdering}
                  <ChevronDown />
                </button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content
                  align="end"
                  className="workspace-directory-ordering-menu"
                  sideOffset={5}
                >
                  {orderingOptions.map((option) => (
                    <DropdownMenu.Item
                      key={option.id}
                      onSelect={() => onOrdering(option.id)}
                    >
                      {option.label}
                      {option.id === ordering && <Check />}
                    </DropdownMenu.Item>
                  ))}
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
          </div>
          <div className="workspace-directory-display-menu__properties">
            <h3>Display properties</h3>
            <div>
              {propertyOptions.map((property) => (
                <button
                  aria-pressed={properties.has(property.id)}
                  key={property.id}
                  onClick={() => onProperty(property.id)}
                  type="button"
                >
                  {property.label}
                </button>
              ))}
            </div>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function FlowFilterIcon() {
  return (
    <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16">
      <path
        clipRule="evenodd"
        d="M14.25 3a.75.75 0 0 1 0 1.5H1.75a.75.75 0 0 1 0-1.5h12.5ZM4 8a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 4 8Zm2.75 3.5a.75.75 0 0 0 0 1.5h2.5a.75.75 0 0 0 0-1.5h-2.5Z"
        fillRule="evenodd"
      />
    </svg>
  );
}

function FlowDisplayIcon() {
  return (
    <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16">
      <path
        clipRule="evenodd"
        d="M7 2.5C8.11933 2.5 9.06613 3.23584 9.38477 4.25H14.75C15.1642 4.25 15.5 4.58579 15.5 5C15.5 5.41421 15.1642 5.75 14.75 5.75H9.38477C9.06613 6.76416 8.11933 7.5 7 7.5C5.88067 7.5 4.93387 6.76416 4.61523 5.75H2.25C1.83579 5.75 1.5 5.41421 1.5 5C1.5 4.58579 1.83579 4.25 2.25 4.25H4.61523C4.93387 3.23584 5.88067 2.5 7 2.5ZM7 4C6.44772 4 6 4.44772 6 5C6 5.55228 6.44772 6 7 6C7.55228 6 8 5.55228 8 5C8 4.44772 7.55228 4 7 4Z"
        fillRule="evenodd"
      />
      <path
        clipRule="evenodd"
        d="M10 13.5C8.88067 13.5 7.93387 12.7642 7.61523 11.75H2.25C1.83579 11.75 1.5 11.4142 1.5 11C1.5 10.5858 1.83579 10.25 2.25 10.25H7.61523C7.93387 9.23584 8.88067 8.5 10 8.5C11.1193 8.5 12.0661 9.23584 12.3848 10.25H14.75C15.1642 10.25 15.5 10.5858 15.5 11C15.5 11.4142 15.1642 11.75 14.75 11.75H12.3848C12.0661 12.7642 11.1193 13.5 10 13.5ZM10 12C10.5523 12 11 11.5523 11 11C11 10.4477 10.5523 10 10 10C9.44772 10 9 10.4477 9 11C9 11.5523 9.44772 12 10 12Z"
        fillRule="evenodd"
      />
    </svg>
  );
}

function FlowAdvancedFilterIcon() {
  return (
    <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16">
      <path d="M6.21973 10.749V8.7793H5.5C4.24079 8.7793 3.21973 7.75823 3.21973 6.49902V4.5293H1.75C1.31922 4.5293.969727 4.17981.969727 3.74902.969727 3.31824 1.31922 2.96875 1.75 2.96875L12.25 2.96875C12.6808 2.96875 13.0303 3.31824 13.0303 3.74902 13.0303 4.17981 12.6808 4.5293 12.25 4.5293H4.78027V6.49902C4.78027 6.89667 5.10236 7.21875 5.5 7.21875L14.25 7.21875C14.6808 7.21875 15.0303 7.56824 15.0303 7.99902 15.0303 8.42981 14.6808 8.7793 14.25 8.7793H7.78027V10.749C7.78027 11.1467 8.10236 11.4688 8.5 11.4688H12.5C12.9308 11.4688 13.2803 11.8182 13.2803 12.249 13.2803 12.6798 12.9308 13.0293 12.5 13.0293H8.5C7.24079 13.0293 6.21973 12.0082 6.21973 10.749Z" />
    </svg>
  );
}

export function DirectoryPeopleIcon() {
  return (
    <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16">
      <path
        clipRule="evenodd"
        d="M10.9933 10.0137C12.0962 10.0789 12.8652 10.3673 13.4151 10.7998C13.8745 11.1612 14.1329 11.5958 14.2901 11.9014L14.4171 12.1572C14.4895 12.2985 14.5578 12.5105 14.6075 12.6748C14.6647 12.8638 14.725 13.0835 14.7794 13.2881C14.834 13.4937 14.8839 13.6898 14.92 13.834C14.938 13.9059 14.9528 13.9653 14.963 14.0068L14.9786 14.0723C15.0761 14.4747 14.8292 14.8809 14.4269 14.9785C14.0243 15.0759 13.619 14.8283 13.5216 14.4258L13.4649 14.1982C13.4299 14.0584 13.3814 13.8694 13.3292 13.6729C13.2767 13.4754 13.221 13.2747 13.171 13.1094C13.1168 12.9304 13.0863 12.851 13.0831 12.8428L12.9532 12.5869C12.8299 12.3483 12.7123 12.1564 12.4874 11.9795C12.2386 11.7838 11.7871 11.5637 10.9054 11.5117L10.5001 11.5C9.35452 11.5 8.80921 11.7545 8.52451 11.9785C8.2188 12.2193 8.11174 12.4807 7.90342 12.8594C7.908 12.8543 7.87818 12.9233 7.82139 13.1104C7.77177 13.2738 7.71736 13.4731 7.66611 13.6699C7.61516 13.8656 7.56804 14.0541 7.53428 14.1934L7.48057 14.4189C7.38673 14.8224 6.98361 15.0743 6.58018 14.9805C6.17691 14.8866 5.92589 14.4834 6.01963 14.0801L6.07725 13.8398C6.11228 13.6953 6.16022 13.4984 6.21396 13.292C6.26738 13.0869 6.32781 12.8659 6.38584 12.6748C6.43545 12.5114 6.50614 12.2896 6.58896 12.1387C6.72536 11.8909 6.99052 11.2777 7.5958 10.8008C8.22303 10.3069 9.1325 10 10.5001 10L10.9933 10.0137Z"
        fillRule="evenodd"
      />
      <path
        clipRule="evenodd"
        d="M5.71592 6.0127C5.86516 6.02118 6.00774 6.03415 6.14365 6.05078C6.55479 6.10113 6.84732 6.47558 6.79697 6.88672C6.74639 7.29744 6.37281 7.58914 5.96201 7.53906C5.75847 7.51415 5.52088 7.50001 5.24619 7.5C4.09451 7.50011 3.64308 7.75722 3.43955 7.94336C3.32997 8.04366 3.24909 8.16093 3.17197 8.30859C3.13166 8.3858 3.09633 8.46418 3.05283 8.55859C3.01292 8.64521 2.96118 8.75425 2.90342 8.85938C2.908 8.85434 2.87818 8.92333 2.82139 9.11035C2.77177 9.27378 2.71736 9.47313 2.66611 9.66992C2.61516 9.86558 2.56804 10.0541 2.53428 10.1934L2.48057 10.4189C2.38673 10.8224 1.98361 11.0743 1.58018 10.9805C1.17691 10.8866.925892 10.4834 1.01963 10.0801L1.07725 9.83984C1.11228 9.69532 1.16022 9.49838 1.21396 9.29199C1.26738 9.08686 1.32781 8.86594 1.38584 8.6748C1.43545 8.5114 1.50614 8.28958 1.58896 8.13867C1.61712 8.08751 1.64705 8.02502 1.69053 7.93066C1.73061 7.84367 1.78222 7.73041 1.84287 7.61426C1.96769 7.37529 2.14427 7.09444 2.42686 6.83594C3.00841 6.30406 3.88547 6.00003 5.24717 6L5.71592 6.0127Z"
        fillRule="evenodd"
      />
      <path d="M10.5001 5C11.6047 5 12.5001 5.89543 12.5001 7C12.5001 8.10457 11.6047 9 10.5001 9C9.39558 8.99994 8.5001 8.10453 8.5001 7C8.5001 5.89547 9.39558 5.00006 10.5001 5Z" />
      <path d="M5.5001 1C6.60467 1 7.5001 1.89543 7.5001 3C7.5001 4.10457 6.60467 5 5.5001 5C4.39558 4.99994 3.5001 4.10453 3.5001 3C3.5001 1.89547 4.39558 1.00006 5.5001 1Z" />
    </svg>
  );
}

export function DirectoryCreatedDateIcon() {
  return (
    <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16">
      <path d="M11 1C13.2091 1 15 2.79086 15 5V6.25C15 6.66421 14.6642 7 14.25 7C13.8358 7 13.5 6.66421 13.5 6.25V6H2.5V11C2.5 12.3807 3.61929 13.5 5 13.5H6.25C6.66421 13.5 7 13.8358 7 14.25C7 14.6642 6.66421 15 6.25 15H5C2.79086 15 1 13.2091 1 11V5C1 2.79086 2.79086 1 5 1H11Z" />
      <path d="M13.2633 8.13808L9.37793 12.0487C10.1422 12.2768 10.7617 12.8515 11.0497 13.5954L14.8764 9.74799C15.1693 9.4815 14.8839 8.6406 14.6178 8.37776C14.3395 8.10295 13.5148 7.82593 13.2633 8.13808Z" />
      <path d="M10.2505 14.3635C10.166 13.5465 9.48171 12.9178 8.66337 12.9077C8.41558 13.3992 8.16295 14.1567 8.01335 14.6377C7.94869 14.8456 8.12874 15.0407 8.34173 14.9926C8.90202 14.8662 9.82253 14.6304 10.2505 14.3635Z" />
    </svg>
  );
}

export function DirectoryLockIcon() {
  return (
    <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16">
      <use href="/flow-core-icons.svg#Lock" />
    </svg>
  );
}
