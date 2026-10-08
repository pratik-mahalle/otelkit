package build

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pratikmahalle/otelkit/internal/diff"
	"github.com/pratikmahalle/otelkit/internal/model"
)

// Check returns warnings for a merged config: processor order, undefined references and unused components.
func Check(m Merged) []string {
	root := m.Root
	var warns []string
	defined := map[string]map[string]bool{}
	for _, k := range model.Kinds {
		defined[k] = map[string]bool{}
		for _, id := range model.Keys(model.Lookup(root, k)) {
			defined[k][id] = true
		}
	}
	used := map[string]bool{}
	pipes := model.Pipelines(root)
	for _, name := range slices.Sorted(maps.Keys(pipes)) {
		p := pipes[name]
		if i := indexOfType(p.Processors, "memory_limiter"); i > 0 {
			warns = append(warns, fmt.Sprintf("pipeline %s: memory_limiter should be the first processor", name))
		}
		if i := indexOfType(p.Processors, "batch"); i >= 0 && i != len(p.Processors)-1 {
			warns = append(warns, fmt.Sprintf("pipeline %s: batch should be the last processor", name))
		}
		ref := func(field string, ids []string, kinds ...string) {
			for _, id := range ids {
				found := false
				for _, k := range kinds {
					if defined[k][id] {
						used[k+"/"+id], found = true, true
					}
				}
				if !found {
					from := m.Origins[diff.Join([]string{"service", "pipelines", name, field})]
					warns = append(warns, fmt.Sprintf("pipeline %s references undefined %s %q (set in %s)",
						name, strings.TrimSuffix(field, "s"), id, from))
				}
			}
		}
		ref("receivers", p.Receivers, "receivers", "connectors")
		ref("processors", p.Processors, "processors")
		ref("exporters", p.Exporters, "exporters", "connectors")
	}
	for _, id := range model.Scalars(model.Get(root, "service", "extensions")) {
		used["extensions/"+id] = true
		if !defined["extensions"][id] {
			warns = append(warns, fmt.Sprintf("service references undefined extension %q", id))
		}
	}
	for _, k := range model.Kinds {
		for _, id := range model.Keys(model.Lookup(root, k)) {
			if !used[k+"/"+id] {
				warns = append(warns, fmt.Sprintf("%s %q is defined but not used", strings.TrimSuffix(k, "s"), id))
			}
		}
	}
	return warns
}

// indexOfType returns the position of the first component of type typ (ignoring /name), or -1.
func indexOfType(ids []string, typ string) int {
	return slices.IndexFunc(ids, func(id string) bool {
		t, _, _ := strings.Cut(id, "/")
		return t == typ
	})
}
