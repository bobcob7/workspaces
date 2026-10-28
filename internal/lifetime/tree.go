package lifetime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// ErrChildKept means a workspace was kept because one of its descendants
// couldn't be removed, so removing it would leave that descendant
// without the parent that removes it (ADR 0044).
var ErrChildKept = errors.New("kept because a workspace it started couldn't be removed")

// TreeOptions are ws rm's options for one workspace and its descendants.
type TreeOptions struct {
	// DryRun makes the same checks and changes nothing.
	DryRun bool
	// KeepChildren makes the workspace's children roots instead of
	// removing them.
	KeepChildren bool
}

// TreeItem is one workspace of a tree ws rm was asked to remove: removed,
// or with dry run removable, unless Err says why not.
type TreeItem struct {
	Removed
	Err error
}

// children maps each workspace id in list to its children's, oldest first.
// A child whose parent isn't in list is a root.
func children(list []workspace.Workspace) map[string][]int {
	index := make(map[string]bool, len(list))
	for i := range list {
		index[list[i].ID] = true
	}
	kids := map[string][]int{}
	for i := range list {
		if p := list[i].Parent; p != "" && p != list[i].ID && index[p] {
			kids[p] = append(kids[p], i)
		}
	}
	return kids
}

// postOrder is the indexes of root's descendants, deepest first, and then
// root. seen holds the indexes already visited, and guards against a
// cycle, which ws never writes but a hand-edited record could.
func postOrder(list []workspace.Workspace, kids map[string][]int, root int, seen map[int]bool) []int {
	var order []int
	var visit func(i int)
	visit = func(i int) {
		if seen[i] {
			return
		}
		seen[i] = true
		for _, c := range kids[list[i].ID] {
			visit(c)
		}
		order = append(order, i)
	}
	visit(root)
	return order
}

// RemoveTree removes ws and its descendants, deepest first, each as Remove
// does (ADR 0044). A descendant that can't be removed keeps itself and
// its ancestors, each with ErrChildKept, while other branches still go.
// With KeepChildren, ws's children become roots and only ws is removed.
// It returns the descendants first and ws last.
func (m *Manager) RemoveTree(ctx context.Context, ws *workspace.Workspace, opts TreeOptions) ([]TreeItem, error) {
	list, err := m.store.List()
	if err != nil {
		return nil, err
	}
	root := -1
	for i := range list {
		if list[i].ID == ws.ID {
			root = i
		}
	}
	if root < 0 {
		list, root = append(list, *ws), len(list)
	}
	kids := children(list)
	if opts.KeepChildren {
		return m.removeKeepingChildren(ctx, list, kids[ws.ID], &list[root], opts.DryRun)
	}
	var items []TreeItem
	failed := map[int]bool{}
	for _, i := range postOrder(list, kids, root, map[int]bool{}) {
		item := m.removeNode(ctx, &list[i], kidsFailed(list, kids, i, failed), opts.DryRun)
		failed[i] = item.Err != nil
		items = append(items, item)
	}
	return items, nil
}

// kidsFailed is the id of a child of node i that failed, or "".
func kidsFailed(list []workspace.Workspace, kids map[string][]int, i int, failed map[int]bool) string {
	for _, c := range kids[list[i].ID] {
		if failed[c] {
			return list[c].ID
		}
	}
	return ""
}

func (m *Manager) removeNode(ctx context.Context, ws *workspace.Workspace, keptChild string, dryRun bool) TreeItem {
	if keptChild != "" {
		return TreeItem{Workspace: *ws, Err: fmt.Errorf("%w: %s", ErrChildKept, keptChild)}
	}
	r, err := m.Remove(ctx, ws, dryRun)
	if err != nil {
		return TreeItem{Workspace: *ws, Err: err}
	}
	return TreeItem{Removed: r}
}

// removeKeepingChildren makes each child a root, then removes ws. A dry
// run changes neither.
func (m *Manager) removeKeepingChildren(ctx context.Context, list []workspace.Workspace, kids []int, ws *workspace.Workspace, dryRun bool) ([]TreeItem, error) {
	if !dryRun {
		for _, c := range kids {
			if _, err := m.store.ClearParent(list[c].ID); err != nil {
				return nil, fmt.Errorf("keeping workspace %s: %w", list[c].ID, err)
			}
		}
	}
	return []TreeItem{m.removeNode(ctx, ws, "", dryRun)}, nil
}

// forestOrder is every index of list, each workspace's descendants before
// it: roots' trees in list order, then anything a cycle left out.
func forestOrder(list []workspace.Workspace, kids map[string][]int) []int {
	isChild := map[int]bool{}
	for _, cs := range kids {
		for _, c := range cs {
			isChild[c] = true
		}
	}
	seen := map[int]bool{}
	var order []int
	for i := range list {
		if !isChild[i] {
			order = append(order, postOrder(list, kids, i, seen)...)
		}
	}
	for i := range list {
		order = append(order, postOrder(list, kids, i, seen)...)
	}
	return order
}

// doomed marks the workspaces the removal pass removes: those whose own
// verdict isn't keep, and all their descendants (ADR 0044).
func doomed(list []workspace.Workspace, kids map[string][]int, verdicts []verdict) []bool {
	out := make([]bool, len(list))
	for i := range list {
		if verdicts[i] == keep || out[i] {
			continue
		}
		for _, d := range postOrder(list, kids, i, map[int]bool{}) {
			out[d] = true
		}
	}
	return out
}

// cascade removes the doomed workspaces, descendants first, and reports
// which are gone. A workspace whose own session is gone is reaped; a
// descendant whose session still exists is removed as ws rm would. A
// workspace whose descendant was kept is kept too, with a warning, and
// the next pass tries again.
func (m *Manager) cascade(ctx context.Context, r *reader, list []workspace.Workspace, verdicts []verdict) []bool {
	kids := children(list)
	doom := doomed(list, kids, verdicts)
	gone := make([]bool, len(list))
	for _, i := range forestOrder(list, kids) {
		if !doom[i] {
			continue
		}
		ws := &list[i]
		if kept := keptChild(list, kids[ws.ID], gone); kept != "" {
			m.warn("kept workspace %s: %v: %s", ws.ID, ErrChildKept, kept)
			continue
		}
		if verdicts[i] != keep {
			gone[i] = m.reap(ctx, r, ws, verdicts[i])
			continue
		}
		if _, err := m.Remove(ctx, ws, false); err != nil {
			m.warn("couldn't remove workspace %s with its parent %s: %v", ws.ID, ws.Parent, err)
			continue
		}
		m.log.InfoContext(ctx, "removed workspace with its parent", slog.String("id", ws.ID), slog.String("parent", ws.Parent))
		gone[i] = true
	}
	return gone
}

// keptChild is the id of a child, by index, that isn't gone, or "".
func keptChild(list []workspace.Workspace, kids []int, gone []bool) string {
	for _, c := range kids {
		if !gone[c] {
			return list[c].ID
		}
	}
	return ""
}
