package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The bounded workflow writer owns its full-record attempt and never reaches
// the shared scoped kernel. Its only ordinal profile is integration delivery.
// The workflow differentials do not certify any of the kernel's profiles.
func TestWorkflowWriterKernelBoundary(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	callers := map[string][]string{}
	var workflowCalls []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(set, path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				name := selector.Sel.Name
				if name == "mutateMetadataOrdinalProfile" || name == "mutateWorkspaceScopedWithAggregate" {
					callers[name] = append(callers[name], path+":"+function.Name.Name)
				}
				if path == "workflow_run_write.go" || path == "workflow_run_point.go" ||
					path == "workflow_run_inputs.go" || path == "workflow_schedule_read.go" {
					workflowCalls = append(workflowCalls, name)
				}
				return true
			})
		}
	}
	slices.Sort(callers["mutateWorkspaceScopedWithAggregate"])
	if !slices.Equal(callers["mutateWorkspaceScopedWithAggregate"], []string{
		// Saved views use the nil ordinal profile and select the target and
		// preference rows after checking the authorization proof under the
		// write lock. The kernel then commits a streamed scoped delta.
		"bounded_views_personal.go:MutateSavedViewRecords",
		// Integration delivery is the only caller that supplies an ordinal factory.
		"metadata_ordinal_profile.go:mutateMetadataOrdinalProfile",
		// The public scoped adapter supplies the declared record scope and no factory.
		"scoped_access.go:MutateWorkspaceScopedWithAggregate",
	}) {
		t.Fatal("shared scoped kernel gained a caller; recertify its profiles", callers["mutateWorkspaceScopedWithAggregate"])
	}
	for _, caller := range callers["mutateMetadataOrdinalProfile"] {
		if !strings.HasPrefix(caller, "integration_delivery") {
			t.Fatal("ordinal kernel profile reached outside integration delivery", caller)
		}
	}
	if len(callers["mutateMetadataOrdinalProfile"]) == 0 {
		t.Fatal("integration delivery no longer uses the ordinal profile; update the kernel inventory note")
	}
	for _, forbidden := range []string{
		"mutateMetadataOrdinalProfile", "mutateWorkspaceScopedWithAggregate", "MutateWorkspace", "MutateWorkspaceWithAggregate",
		"MutateWorkspaceScoped", "MutateWorkspaceScopedWithAggregate", "Bootstrap", "BootstrapFor", "WorkspaceMetadata",
		"WorkspaceMetadataFields", "PagedWorkspaceMetadata", "decodeWorkspaceMetadata", "loadWorkspaceState", "mutateFull",
	} {
		if slices.Contains(workflowCalls, forbidden) {
			t.Fatal("bounded workflow writer/reader reached a full-workspace or shared-kernel accessor", forbidden)
		}
	}
}
