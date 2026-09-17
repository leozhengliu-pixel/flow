package store

import "flow/api/internal/domain"

// Keep picker and navigation metadata, but load resource bodies on their own
// detail routes. Issue history/body already have paged record endpoints.
func IssueDetailBootstrapProjection(data *domain.Bootstrap) {
	data.ResourceDetailsOmitted = true
	for index := range data.Projects {
		data.Projects[index] = ProjectListProjection(data.Projects[index])
	}
	for index := range data.Documents {
		doc := &data.Documents[index]
		doc.Content, doc.ContentState = "", ""
		doc.ContentData, doc.Revisions = nil, nil
	}
	data.Comments = map[string][]domain.Comment{}
	data.Activities = map[string][]domain.ActivityEvent{}
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	data.ProviderJobs = nil
}
