package main

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) searchQuery(r *http.Request)(domain.Bootstrap,store.IssueRecordQuery,error){
	data,q,err:=s.issueRecordsQuery(r)
	if err!=nil{return data,q,err}
	if s.authDisabled { if viewer,ok:=s.store.WorkspaceSearchViewer(q.Workspace);ok {data.Viewer=viewer} }
	q.Archived="false";if r.URL.Query().Get("includeArchived")=="true" {q.Archived="all"}
	nodes:=[]store.IssueFilter{q.Filter}
	for _,item:=range []struct{parameter,field string}{{"statusType","statusType"},{"assigneeId","assignee"},{"creatorId","creator"}} {if value:=r.URL.Query().Get(item.parameter);value!="" {nodes=append(nodes,store.IssueFilter{Field:item.field,Values:splitQueryValues(value)})} }
	for _,item:=range []struct{parameter,field,operator string}{{"createdAfter","createdAt","gte"},{"createdBefore","createdAt","lte"},{"updatedAfter","updatedAt","gte"},{"updatedBefore","updatedAt","lte"}} {
		if value:=r.URL.Query().Get(item.parameter);value!="" {date,err:=time.Parse(time.RFC3339Nano,value);if err!=nil{date,err=time.Parse("2006-01-02",value)};if err!=nil{return data,q,store.ErrIssueQuery};if len(value)==10&&item.operator=="lte"{date=date.Add(24*time.Hour-time.Nanosecond)};nodes=append(nodes,store.IssueFilter{Field:item.field,Operator:item.operator,Values:[]string{date.UTC().Format(time.RFC3339Nano)}})}
	}
	q.Filter=store.IssueFilter{And:nodes}
	q.Sort=r.URL.Query().Get("sort");if q.Sort=="relevance"{q.Sort=""}
	if q.Sort!=""&&!slices.Contains([]string{"title","createdAt","updatedAt"},q.Sort){return data,q,store.ErrIssueQuery}
	q.Direction="desc";if q.Sort=="title"{q.Direction="asc"}
	return data,q,nil
}

func searchResultOrder(results []domain.SearchResult,q store.IssueRecordQuery){
	if q.Sort=="" {sortSearchResults(results);return}
	slices.SortStableFunc(results,func(a,b domain.SearchResult)int{
		if q.Sort=="title" {return strings.Compare(strings.ToLower(a.Title),strings.ToLower(b.Title))}
		if q.Sort=="createdAt" {return b.CreatedAt.Compare(a.CreatedAt)}
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
}

func enrichSearchResults(results []domain.SearchResult,data domain.Bootstrap){
	issues:=map[string]domain.Issue{};projects:=map[string]domain.Project{}
	for _,issue:=range data.Issues{issues[issue.ID]=issue};for _,project:=range data.Projects{projects[project.ID]=project}
	for i:=range results {r:=&results[i];if issue,ok:=issues[r.ID];r.Type=="issue"&&ok {state:=issue.State;r.State=&state;r.StatusType=state.Type;r.StatusName=state.Name;r.CreatedAt=issue.CreatedAt};if project,ok:=projects[r.ID];r.Type=="project"&&ok {status:=project.Status;r.ProjectStatus=&status;r.StatusType=status.Type;r.StatusName=status.Name;r.CreatedAt=project.CreatedAt} }
}
