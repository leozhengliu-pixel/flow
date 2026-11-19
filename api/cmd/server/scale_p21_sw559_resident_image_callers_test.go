package main

import (
	"go/ast"
	"go/types"
	"sort"
	"testing"
)

// SW559 (store/scale_p21_sw559_resident_image_readers_test.go) reviews the
// store accessors that read a non-resident field of the installed image. A
// few are legacy accessors left reading the image because every server call
// of them runs with bounded metadata off; this pins that: each call (or
// method value) of them in this package must be flag-off-only by the SW270
// rules, or sit in a function only flag-off-only references reach.
var scaleP21SW559FlagOffAccessors = []string{"AccountCollections", "MetadataRecord", "TrashRecordHeader", "OAuthApplicationsWithLogo"}

// scaleP21SW559FlagOnReachable answers the package functions that can run
// with bounded metadata on: every function no reference names (entries such
// as main, init and dispatch from imported code) and every function a
// reachable one names outside a flag-off-only branch.
func scaleP21SW559FlagOnReachable(g *scaleP21SW270Graph) map[string]bool {
	reached := map[string]bool{}
	var queue []string
	for key := range g.funcs {
		if len(g.into[key]) == 0 {
			reached[key] = true
			queue = append(queue, key)
		}
	}
	out := map[string][]int{}
	for index, ref := range g.refs {
		out[ref.from] = append(out[ref.from], index)
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, index := range out[key] {
			ref := g.refs[index]
			if ref.offOnly || reached[ref.to] {
				continue
			}
			reached[ref.to] = true
			queue = append(queue, ref.to)
		}
	}
	return reached
}

func TestScaleP21SW559FlagOffAccessorCallers(t *testing.T) {
	g := scaleP21SW270Load(t, ".")
	reached := scaleP21SW559FlagOnReachable(g)
	names := map[string]bool{}
	for _, name := range scaleP21SW559FlagOffAccessors {
		names[name] = true
	}
	var found, uses []string
	for key, fn := range g.funcs {
		var ancestors []ast.Node
		ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
			if node == nil {
				ancestors = ancestors[:len(ancestors)-1]
				return false
			}
			ancestors = append(ancestors, node)
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || !names[selector.Sel.Name] {
				return true
			}
			if ident, ok := selector.X.(*ast.Ident); ok {
				if _, imported := g.info.Uses[ident].(*types.PkgName); imported {
					return true // a type (store.MetadataRecord), not the method
				}
			}
			position := g.fset.Position(selector.Pos())
			use := key + " " + selector.Sel.Name
			uses = append(uses, use)
			if !g.offOnly(ancestors) && reached[key] {
				found = append(found, use+" "+position.String())
			}
			return true
		})
	}
	sort.Strings(found)
	for _, use := range found {
		t.Errorf("SW559: a flag-off legacy accessor is reachable with bounded metadata on (it reads a field the bounded image does not hold): %s", use)
	}
	if len(uses) == 0 {
		t.Fatal("SW559: no use of the flag-off accessors found; the walk is stale")
	}
}
