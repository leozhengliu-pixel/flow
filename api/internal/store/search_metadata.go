package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

type SearchMetadataQuery struct {
	Scope IssueRecordQuery
	Types map[string]bool
	Terms []string
	Recent []domain.RecentResource
	Limit int
}

type searchMetadataKind struct { kind, field string; fields, text []string }

var searchMetadataKinds = []searchMetadataKind{
	{"project", "projects", []string{"name","summary","description","icon","color","status","lead","teamIds"}, []string{"name","summary","description"}},
	{"document", "documents", []string{"title","content","icon","color","creator","teamIds","projectIds","permissions"}, []string{"title","content"}},
	{"initiative", "initiatives", []string{"name","summary","description","icon","color","status","health","creator","owner","leadTeamId","contributingTeamIds","projectIds"}, []string{"name","summary","description"}},
	{"member", "users", []string{"name","displayName","email","active"}, []string{"name","displayName","email"}},
	{"customer", "customers", []string{"name","domains","ownerId","status","tier"}, []string{"name","domains","status","tier"}},
	{"release", "releases", []string{"name","version","description","status","creator","pipelineId","projectIds","issueIds"}, []string{"name","version","description","status"}},
	{"view", "savedViews", []string{"name","description","icon","color","scope","resource","teamId","projectId","ownerId"}, []string{"name","description","scope","resource"}},
}

// Search reads only matching, authorized shells in keyset batches. Large editor
// state, revisions, discussions and project resource collections never leave SQL.
func (s *SQLiteStore) SearchMetadata(ctx context.Context, policy domain.Bootstrap, q SearchMetadataQuery) (domain.Bootstrap, error) {
	result := domain.Bootstrap{Workspace: policy.Workspace, Viewer: policy.Viewer, ViewerRole: policy.ViewerRole}
	allowed := map[string]bool{}
	for _, team := range policy.Teams { if (q.Scope.Access == nil || q.Scope.Access.Admin || slices.Contains(q.Scope.Access.VisibleTeamIDs, team.ID)) && (q.Scope.AllowedTeamIDs == nil || slices.Contains(q.Scope.AllowedTeamIDs, team.ID)) { allowed[team.ID] = true } }
	limit := q.Limit; if limit < 1 || limit > 500 { limit = 100 }
	for _, kind := range searchMetadataKinds {
		if !q.Types[kind.kind] { continue }
		where, args := "workspace_key=? AND field=?", []any{q.Scope.Workspace,kind.field}
		if len(q.Terms) == 0 {
			ids := []string{}
			for _, recent := range q.Recent { if recent.ResourceType == kind.kind { ids = append(ids, recent.ResourceID) } }
			if len(ids) == 0 { continue }
			clause, values := bindList("record_key",ids); where += " AND "+clause; args = append(args,values...)
		} else {
			matches := []string{}
			for _, term := range q.Terms { for _, field := range kind.text { matches = append(matches, "LOWER(COALESCE("+s.jsonText("data",field)+",'')) LIKE ? ESCAPE '!'"); args=append(args,"%"+escapeIssueLike(strings.ToLower(term))+"%") } }
			where += " AND ("+strings.Join(matches," OR ")+")"
		}
		if q.Scope.Archived != "all" && q.Scope.Archived != "true" { where += " AND "+s.jsonText("data","archivedAt")+" IS NULL" }
		if len(q.Scope.TeamIDs)>0 {
			clauses:=[]string{}
			for _, id:=range q.Scope.TeamIDs { for _, field:=range []string{"teamIds","teamId","leadTeamId","contributingTeamIds"} { clauses=append(clauses,"COALESCE("+s.jsonText("data",field)+",'') LIKE ? ESCAPE '!'"); args=append(args,"%"+escapeIssueLike(id)+"%") } }
			where += " AND ("+strings.Join(clauses," OR ")+")"
		}
		clause, values, err := s.searchMetadataFilter(kind.kind,q.Scope.Filter,0)
		if err != nil { return result,err }
		where += " AND "+clause; args=append(args,values...)
		fields:=append([]string{"id","slugId","createdAt","updatedAt","archivedAt"},kind.fields...)
		columns:=make([]string,len(fields))
		for i,field:=range fields { columns[i]=s.jsonText("data",field); if field=="description"||field=="content" { columns[i]="SUBSTR("+columns[i]+",1,2048)" } }
		sortField,direction:="updatedAt","DESC"
		if q.Scope.Sort=="createdAt" { sortField="createdAt" }
		if q.Scope.Sort=="title" { sortField="name"; if kind.kind=="document" { sortField="title" }; if kind.kind=="member" { sortField="displayName" }; direction="ASC" }
		if q.Scope.Direction=="asc" { direction="ASC" }
		orderExpr:="COALESCE("+s.jsonText("data",sortField)+",'')"
		lastValue,lastID:="",""
		accepted:=0
		for accepted<limit {
			pageWhere,pageArgs:=where,slices.Clone(args)
			if lastID!="" { op:="<";if direction=="ASC" { op=">" };pageWhere+=" AND ("+orderExpr+op+"? OR ("+orderExpr+"=? AND record_key>?))";pageArgs=append(pageArgs,lastValue,lastValue,lastID) }
			rows,err:=s.db.QueryContext(ctx,"SELECT record_key,"+orderExpr+","+strings.Join(columns,",")+" FROM workspace_metadata_records WHERE "+pageWhere+" ORDER BY "+orderExpr+" "+direction+",record_key ASC LIMIT 64",pageArgs...)
			if err!=nil { return result,err }
			batch:=[][]byte{}
			for rows.Next() {
				values:=make([]sql.NullString,len(fields));dest:=[]any{&lastID,&lastValue};for i:=range values {dest=append(dest,&values[i])}
				if err:=rows.Scan(dest...);err!=nil {rows.Close();return result,err}
				object:=map[string]json.RawMessage{}
				for i,value:=range values { if !value.Valid {continue}; field:=fields[i]; if searchMetadataJSONField(field,kind.kind) {object[field]=json.RawMessage(value.String)} else {raw,_:=json.Marshal(value.String);object[field]=raw} }
				raw,err:=json.Marshal(object);if err!=nil {rows.Close();return result,err};batch=append(batch,raw)
			}
			err=rows.Err();rows.Close();if err!=nil {return result,err}
			for _,raw:=range batch { ok,err:=s.appendSearchMetadata(ctx,&result,policy,q.Scope,allowed,kind.kind,raw);if err!=nil {return result,err};if ok {accepted++};if accepted>=limit {break} }
			if len(batch)<64 {break}
		}
	}
	return result,nil
}

func searchMetadataJSONField(field,kind string) bool {
	return slices.Contains([]string{"teamIds","projectIds","issueIds","contributingTeamIds","permissions","domains","creator","lead","owner","active"},field)||field=="status"&&kind=="project"
}

func (s *SQLiteStore) searchMetadataFilter(kind string,node IssueFilter,depth int)(string,[]any,error){
	if depth>8{return "",nil,ErrIssueQuery}
	clauses,args:=[]string{},[]any{}
	for _,group:=range []struct{nodes []IssueFilter;join string}{{node.And," AND "},{node.Or," OR "}} {parts:=[]string{};for _,child:=range group.nodes {clause,values,err:=s.searchMetadataFilter(kind,child,depth+1);if err!=nil{return "",nil,err};parts=append(parts,clause);args=append(args,values...)};if len(parts)>0 {clauses=append(clauses,"("+strings.Join(parts,group.join)+")")} }
	if node.Field!="" {
		field:=map[string]string{"id":"id","title":"name","createdAt":"createdAt","updatedAt":"updatedAt","creator":"creator.id","creatorId":"creator.id","assignee":"lead.id","assigneeId":"lead.id","status":"status.id","stateId":"status.id","statusType":"status.type"}[node.Field]
		if field=="" {return "0=1",nil,nil}
		if field=="lead.id"&&kind=="initiative" {field="owner.id"}
		column:=s.jsonText("data",field)
		op:=strings.ToLower(node.Operator);if op=="" {op="is"}
		switch op {
		case "is","in","isnot","notin":clause,values:=bindList("COALESCE("+column+",'')",node.Values);if op=="isnot"||op=="notin" {clause="NOT ("+clause+")"};clauses=append(clauses,clause);args=append(args,values...)
		case "before","after","gte","lte","gt","lt":if len(node.Values)!=1{return "",nil,ErrIssueQuery};operator:=map[string]string{"before":"<","after":">","gte":">=","lte":"<=","gt":">","lt":"<"}[op];clauses=append(clauses,column+operator+"?");args=append(args,node.Values[0])
		case "isempty":clauses=append(clauses,"COALESCE("+column+",'')=''")
		case "isnotempty":clauses=append(clauses,"COALESCE("+column+",'')<>''")
		default:return "",nil,ErrIssueQuery
		}
	}
	if len(clauses)==0{return "1=1",nil,nil};return "("+strings.Join(clauses," AND ")+")",args,nil
}

func (s *SQLiteStore) appendSearchMetadata(ctx context.Context,result *domain.Bootstrap,policy domain.Bootstrap,q IssueRecordQuery,allowed map[string]bool,kind string,raw []byte)(bool,error){
	teamAllowed:=func(ids []string)bool {return len(ids)==0&&q.AllowedTeamIDs==nil||slices.ContainsFunc(ids,func(id string)bool{return allowed[id]})}
	projectAllowed:=func(ids []string)(bool,error){refs:=[]domain.Issue{};for _,id:=range ids{refs=append(refs,domain.Issue{Project:&domain.ProjectSummary{ID:id}})};data,err:=s.IssueReferenceMetadata(ctx,q,refs);return len(data.Projects)==len(ids),err}
	admin:=q.Access==nil||q.Access.Admin
	switch kind {
	case "project":var item domain.Project;if err:=json.Unmarshal(raw,&item);err!=nil{return false,err};if !teamAllowed(item.TeamIDs){return false,nil};result.Projects=append(result.Projects,item)
	case "document":var item domain.Document;if err:=json.Unmarshal(raw,&item);err!=nil{return false,err};if !admin && !(len(item.TeamIDs)==0&&len(item.Permissions)==0 || slices.ContainsFunc(item.TeamIDs,func(id string)bool{return allowed[id]}) || documentPermissionAllows(&policy,item,allowed)){return false,nil};if q.AllowedTeamIDs!=nil&&!teamAllowed(item.TeamIDs){return false,nil};result.Documents=append(result.Documents,item)
	case "initiative":var item domain.Initiative;if err:=json.Unmarshal(raw,&item);err!=nil{return false,err};if item.LeadTeamID!=""&&!allowed[item.LeadTeamID]{return false,nil};visible:=item.LeadTeamID!=""||slices.ContainsFunc(item.ContributingTeamIDs,func(id string)bool{return allowed[id]});if !visible&&len(item.ProjectIDs)>0{for _,id:=range item.ProjectIDs {ok,err:=projectAllowed([]string{id});if err!=nil{return false,err};if ok{visible=true;break}}};if !visible&&(len(item.ContributingTeamIDs)>0||len(item.ProjectIDs)>0){return false,nil};result.Initiatives=append(result.Initiatives,item)
	case "member":var item domain.User;if err:=json.Unmarshal(raw,&item);err!=nil{return false,err};if policy.ViewerRole=="guest"&&item.ID!=policy.Viewer.ID{return false,nil};result.Users=append(result.Users,item)
	case "customer":if policy.ViewerRole=="guest"{return false,nil};var item domain.Customer;if err:=json.Unmarshal(raw,&item);err!=nil{return false,err};result.Customers=append(result.Customers,item)
	case "release":var item domain.Release;if err:=json.Unmarshal(raw,&item);err!=nil{return false,err};ok,err:=projectAllowed(item.ProjectIDs);if err!=nil||!ok{return false,err};if item.PipelineID!=""{var value string;err:=s.db.QueryRowContext(ctx,"SELECT "+s.jsonText("data","teamIds")+" FROM workspace_metadata_records WHERE workspace_key=? AND field='releasePipelines' AND record_key=?",q.Workspace,item.PipelineID).Scan(&value);if err==sql.ErrNoRows{return false,nil};if err!=nil{return false,err};var ids []string;if err=json.Unmarshal([]byte(value),&ids);err!=nil{return false,err};if !teamAllowed(ids){return false,nil}};visible,err:=s.VisibleIssueRecordIDs(ctx,q,item.IssueIDs);if err!=nil||len(visible)!=len(item.IssueIDs){return false,err};result.Releases=append(result.Releases,item)
	case "view":var item domain.SavedView;if err:=json.Unmarshal(raw,&item);err!=nil{return false,err};if item.Scope=="team"&&!allowed[item.TeamID]||item.Scope=="personal"&&item.OwnerID!=policy.Viewer.ID{return false,nil};if item.ProjectID!=""{ok,err:=projectAllowed([]string{item.ProjectID});if err!=nil||!ok{return false,err}};result.SavedViews=append(result.SavedViews,item)
	}
	return true,nil
}
