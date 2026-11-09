import {
  AlarmClock,
  Archive,
  ArrowRightLeft,
  Bell,
  BellOff,
  Box,
  CalendarDays,
  CircleEllipsis,
  CircleMinus,
  CircleDashed,
  Clipboard,
  Copy,
  CornerLeftUp,
  Diamond,
  Flag,
  ExternalLink,
  GitBranch,
  Hash,
  Link,
  Link2,
  Octagon,
  SquarePlus,
  Users,
  CornerDownRight,
  RefreshCw,
  SignalHigh,
  Star,
  Tag,
  Trash2,
  Triangle,
  Type,
  UserCheck,
  UserRound,
  type LucideIcon,
} from "lucide-react";

import { IssueActionGlyph } from "@/components/issue/issue-action-glyphs";
import { CycleIcon, PriorityIcon } from "@/components/issue/issue-icons";
import { FlowGlyph, type FlowGlyphName } from "@/components/ui/menu-glyphs";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import styles from "./my-issues-list.module.css";

/** Leading icons for top-level issue context menu items, keyed by label. */
const ICONS: Record<string, LucideIcon> = {
  Status: CircleDashed,
  Priority: SignalHigh,
  Assignee: UserRound,
  "Due date": CalendarDays,
  Labels: Tag,
  Project: Box,
  Cycle: RefreshCw,
  Estimate: Triangle,
  Milestone: Diamond,
  "Assign to me": UserCheck,
  "More properties": CircleEllipsis,
  "Create related": SquarePlus,
  "Mark as": Flag,
  "Remove…": CircleMinus,
  "Remove": CircleMinus,
  "Open in": ExternalLink,
  Unfavorite: Star,
  "Team…": Users,
  "Add link…": Link,
  "Parent of…": CornerLeftUp,
  "Sub-issue of…": CornerDownRight,
  "Related to…": Link2,
  "Blocked by…": Octagon,
  "Blocking…": Octagon,
  "Duplicate of…": Copy,
  "Set parent issue…": CornerLeftUp,
  Relations: Link2,
  "Move to team…": ArrowRightLeft,
  "Move": ArrowRightLeft,
  "Convert to": RefreshCw,
  Subscribe: Bell,
  Unsubscribe: BellOff,
  Favorite: Star,
  "Remove from favorites": Star,
  "Remind me": AlarmClock,
  "Copy branch name": GitBranch,
  "Copy as Markdown link": Link,
  "Open in new tab": ExternalLink,
  "Make a copy…": Copy,
  Archive: Archive,
  Copy: Clipboard,
  "Copy issue URL": Link,
  "Copy issue ID": Hash,
  "Copy issue title": Type,
  Delete: Trash2,
};

/** Flow's own glyphs for items that have one; lucide icons fill the rest. */
const GLYPHS: Record<string, string> = {
  Labels: "Label",
  Project: "Project",
  Favorite: "Favorite",
  Unfavorite: "Favorite",
  "Remove from favorites": "Favorite",
  Subscribe: "Subscribe",
  "Remind me": "Alarm",
  "Team…": "Team",
  "Add link…": "Link",
};

/** Menu glyphs drawn from the shared set (muted, fixed shape; they do not follow the issue's own state). */
const MENU_GLYPHS: Record<string, FlowGlyphName> = { Status: "rowStatus", "Due date": "rowDueDate", Move: "rowMove", Unsubscribe: "rowUnsubscribe" };

export function ContextMenuIcon({ label }: { label: string }) {
  const menuGlyph = MENU_GLYPHS[label];
  if (menuGlyph) return <span className={styles.menuIcon} aria-hidden="true"><FlowGlyph name={menuGlyph}/></span>;
  const glyph = GLYPHS[label];
  if (glyph) return <span className={styles.menuIcon} aria-hidden="true"><ViewGlyph icon={glyph} color="currentColor" style={{ width: 16, height: 16 }} /></span>;
  if (label === "Priority") return <span className={styles.menuIcon} aria-hidden="true"><PriorityIcon priority={2} size={16} aria-hidden aria-label={undefined} role={undefined}/></span>;
  if (label === "Assignee") return <span className={styles.menuIcon} aria-hidden="true"><FlowGlyph name="assignee"/></span>;
  if (label === "More properties") return <span className={styles.menuIcon} aria-hidden="true"><FlowGlyph name="moreProperties"/></span>;
  if (label === "Open in") return <span className={styles.menuIcon} aria-hidden="true"><FlowGlyph name="openIn"/></span>;
  if (label === "Cycle") return <span className={styles.menuIcon} aria-hidden="true"><CycleIcon noCycle size={16} aria-hidden/></span>;
  const Icon = ICONS[label];
  const fallback = Icon ? <Icon className={styles.menuIcon} size={16} aria-hidden="true" /> : null;
  const action = IssueActionGlyph({ label, fallback: null });
  return action ? <span className={styles.menuIcon} aria-hidden="true">{action}</span> : fallback;
}
