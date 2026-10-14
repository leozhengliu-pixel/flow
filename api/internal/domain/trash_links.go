package domain

// TrashLinks records what deleting a resource detached from other records
// (issues leaving a deleted project, sub-initiatives losing their parent, a
// customer's requests, ...). Restoring the trash entry re-attaches each link
// whose records still exist, so the resource comes back exactly as it was,
// 's trash. It is persisted with the entry and stripped from
// viewer projections (it can name other members' favorites).
type TrashLinks struct {
	Favorites     []Favorite     `json:"favorites,omitempty"`
	Subscriptions []Subscription `json:"subscriptions,omitempty"`
	Drafts        []Draft        `json:"drafts,omitempty"`

	// Issue deletion. DeletedWithIssueID is the issue whose deletion took
	// this sub-issue with it; restoring that issue restores this one too.
	DeletedWithIssueID     string                 `json:"deletedWithIssueId,omitempty"`
	ChildIssueIDs          []string               `json:"childIssueIds,omitempty"`
	InverseRelations       []IssueRelation        `json:"inverseRelations,omitempty"`
	ReleaseIDs             []string               `json:"releaseIds,omitempty"`
	ProjectTemplateIDs     []string               `json:"projectTemplateIds,omitempty"`
	CustomerRequestIDs     []string               `json:"customerRequestIds,omitempty"`
	DocumentIDs            []string               `json:"documentIds,omitempty"`
	Asks                   []Ask                  `json:"asks,omitempty"`
	IssueSLAs              []IssueSLA             `json:"issueSlas,omitempty"`
	SLAEvents              []SLAEvent             `json:"slaEvents,omitempty"`
	Notifications          []Notification         `json:"notifications,omitempty"`
	NotificationDeliveries []NotificationDelivery `json:"notificationDeliveries,omitempty"`

	// Project deletion.
	ProjectIssues       []TrashProjectIssue `json:"projectIssues,omitempty"`
	DependentProjectIDs []string            `json:"dependentProjectIds,omitempty"`
	ProjectRelations    []ProjectRelation   `json:"projectRelations,omitempty"`

	// Initiative and customer deletion: projects that listed the resource.
	ProjectIDs          []string             `json:"projectIds,omitempty"`
	ChildInitiativeIDs  []string             `json:"childInitiativeIds,omitempty"`
	InitiativeRelations []InitiativeRelation `json:"initiativeRelations,omitempty"`

	// Customer deletion.
	CustomerRequests []CustomerRequest `json:"customerRequests,omitempty"`

	// Document deletion: the project resource rows that pointed at it.
	ProjectResources []ProjectResource `json:"projectResources,omitempty"`

	// Loop deletion.
	LoopVersions []LoopVersion `json:"loopVersions,omitempty"`

	// Release pipeline deletion: the releases deleted (trashed) with the
	// pipeline, restored with it.
	PipelineReleaseIDs []string `json:"pipelineReleaseIds,omitempty"`
}

// TrashProjectIssue is an issue that was in a deleted project, with the
// milestone it was in.
type TrashProjectIssue struct {
	IssueID     string `json:"issueId"`
	MilestoneID string `json:"milestoneId,omitempty"`
	// Archived is set when deleting the project archived the issue (the reference app
	// archives a deleted project's issues); restoring un-archives it.
	Archived bool `json:"archived,omitempty"`
}
