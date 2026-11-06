package domain

import (
	"maps"
	"slices"
	"sync/atomic"
)

// CreationDefaults are the operator's defaults for objects Flow creates: a new
// workspace's settings, a new team's settings and cycles, and a member's
// personal settings and notification preferences when they are first created.
// They come from the `defaults:` section of the configuration file
// (internal/config, docs/configuration.md). A nil field keeps Flow's built-in
// default. They never change a value that is already stored.
type CreationDefaults struct {
	Workspace WorkspaceCreationDefaults
	Team      TeamCreationDefaults
	User      UserCreationDefaults
}

// WorkspaceCreationDefaults apply to the settings of a new workspace.
type WorkspaceCreationDefaults struct {
	FiscalMonth          *string
	GuestsAllowed        *bool
	SessionDurationDays  *int
	InvitePermission     *string
	TeamCreatePermission *string
	InviteLinksEnabled   *bool
	// AllowedDomains is nil when unset; an empty list is a configured value.
	AllowedDomains []string
	// FeatureFlags override single entries of the built-in feature flags.
	FeatureFlags map[string]bool
}

// TeamCreationDefaults apply to a new team that does not copy its settings
// from another team (a sub-team or "copy settings from").
type TeamCreationDefaults struct {
	Timezone                 *string
	EstimateType             *string
	EstimateAllowZero        *bool
	EstimateExtended         *bool
	EstimateCountUnestimated *bool
	DefaultPriority          *int
	DetailedHistory          *bool
	TriageEnabled            *bool
	TriageRequirePriority    *bool
	AutoCloseParents         *bool
	AutoCloseSubIssues       *bool
	AutoCloseStale           *bool
	StaleMonths              *int
	AutoArchiveMonths        *int
	Cycles                   CycleCreationDefaults
}

// CycleCreationDefaults apply to a new team's cycle settings.
type CycleCreationDefaults struct {
	Enabled          *bool
	DurationWeeks    *int
	CooldownWeeks    *int
	StartsOn         *int // 0 Sunday … 6 Saturday
	UpcomingCount    *int
	AutoCreate       *bool
	AutoAddStarted   *bool
	AutoAddCompleted *bool
	AutoMigrate      *bool
}

// UserCreationDefaults apply when a member's personal settings or
// notification preferences in a workspace are first created.
type UserCreationDefaults struct {
	Language       *string
	InterfaceTheme *string
	FirstDay       *string
	HomeView       *string
	DisplayNames   *string
	FontSize       *string
	// Notification channels and the email format.
	EmailNotifications   *bool
	DesktopNotifications *bool
	EmailFormat          *string
}

var creationDefaults atomic.Pointer[CreationDefaults]

// SetCreationDefaults installs the operator's creation defaults (at startup).
func SetCreationDefaults(defaults CreationDefaults) {
	creationDefaults.Store(&defaults)
}

// CurrentCreationDefaults returns the installed creation defaults (empty when
// none were configured).
func CurrentCreationDefaults() CreationDefaults {
	if current := creationDefaults.Load(); current != nil {
		return *current
	}
	return CreationDefaults{}
}

func applyDefault[T any](target *T, value *T) {
	if value != nil {
		*target = *value
	}
}

// Apply writes the configured values over a new workspace's built-in settings.
func (d WorkspaceCreationDefaults) Apply(settings *WorkspaceSettings) {
	applyDefault(&settings.FiscalMonth, d.FiscalMonth)
	applyDefault(&settings.GuestsAllowed, d.GuestsAllowed)
	applyDefault(&settings.SessionDurationDays, d.SessionDurationDays)
	applyDefault(&settings.InvitePermission, d.InvitePermission)
	applyDefault(&settings.TeamCreatePermission, d.TeamCreatePermission)
	applyDefault(&settings.InviteLinksEnabled, d.InviteLinksEnabled)
	if d.AllowedDomains != nil {
		settings.AllowedDomains = slices.Clone(d.AllowedDomains)
	}
	if len(d.FeatureFlags) > 0 {
		flags := maps.Clone(settings.FeatureFlags)
		if flags == nil {
			flags = map[string]bool{}
		}
		maps.Copy(flags, d.FeatureFlags)
		settings.FeatureFlags = flags
	}
}

// Apply writes the configured values over a new team's built-in settings.
func (d TeamCreationDefaults) Apply(settings *TeamSettings) {
	applyDefault(&settings.Timezone, d.Timezone)
	applyDefault(&settings.EstimateType, d.EstimateType)
	applyDefault(&settings.EstimateAllowZero, d.EstimateAllowZero)
	applyDefault(&settings.EstimateExtended, d.EstimateExtended)
	if d.EstimateCountUnestimated != nil {
		value := *d.EstimateCountUnestimated
		settings.EstimateCountUnestimated = &value
	}
	applyDefault(&settings.DefaultPriority, d.DefaultPriority)
	applyDefault(&settings.DetailedHistory, d.DetailedHistory)
	applyDefault(&settings.TriageEnabled, d.TriageEnabled)
	applyDefault(&settings.TriageRequirePriority, d.TriageRequirePriority)
	applyDefault(&settings.AutoCloseParents, d.AutoCloseParents)
	applyDefault(&settings.AutoCloseSubIssues, d.AutoCloseSubIssues)
	applyDefault(&settings.AutoCloseStale, d.AutoCloseStale)
	applyDefault(&settings.StaleMonths, d.StaleMonths)
	applyDefault(&settings.AutoArchiveMonths, d.AutoArchiveMonths)
}

// NewTeamCycleSettings are a new team's cycle settings: Flow's built-in
// defaults (cycles off, two-week cycles starting on Monday) with the
// configured values applied.
func (d TeamCreationDefaults) NewTeamCycleSettings() CycleSettings {
	settings := CycleSettings{Enabled: false, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 4, AutoCreate: true, AutoMigrate: true}
	c := d.Cycles
	applyDefault(&settings.Enabled, c.Enabled)
	applyDefault(&settings.DurationWeeks, c.DurationWeeks)
	applyDefault(&settings.CooldownWeeks, c.CooldownWeeks)
	applyDefault(&settings.StartsOn, c.StartsOn)
	applyDefault(&settings.UpcomingCount, c.UpcomingCount)
	applyDefault(&settings.AutoCreate, c.AutoCreate)
	applyDefault(&settings.AutoAddStarted, c.AutoAddStarted)
	applyDefault(&settings.AutoAddCompleted, c.AutoAddCompleted)
	applyDefault(&settings.AutoMigrate, c.AutoMigrate)
	return settings
}

// Apply writes the configured values over a member's built-in personal settings.
func (d UserCreationDefaults) Apply(settings *UserSettings) {
	applyDefault(&settings.Language, d.Language)
	applyDefault(&settings.InterfaceTheme, d.InterfaceTheme)
	applyDefault(&settings.FirstDay, d.FirstDay)
	applyDefault(&settings.HomeView, d.HomeView)
	applyDefault(&settings.DisplayNames, d.DisplayNames)
	applyDefault(&settings.FontSize, d.FontSize)
}

// ApplyNotifications writes the configured values over a member's built-in
// notification preferences.
func (d UserCreationDefaults) ApplyNotifications(preferences *NotificationPreferences) {
	applyDefault(&preferences.Email.Enabled, d.EmailNotifications)
	applyDefault(&preferences.Desktop.Enabled, d.DesktopNotifications)
	applyDefault(&preferences.EmailFormat, d.EmailFormat)
}
