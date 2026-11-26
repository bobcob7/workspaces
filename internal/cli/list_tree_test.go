package cli

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// rowsDepths is each row's key and depth, in order.
func rowsDepths(rows []listRow) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, fmt.Sprintf("%s@%d", rowKey(rows[i].env, rows[i].id), rows[i].depth))
	}
	return out
}

func TestCrossOrder(t *testing.T) {
	t.Run("nests a remote child under its local parent", func(t *testing.T) {
		rows := []listRow{
			{env: localEnvironment, id: "plan-1"},
			{env: "linux-server", id: "ip-1", scopeParent: rowKey(localEnvironment, "plan-1")},
		}
		assert.Equal(t, []string{"local/plan-1@0", "linux-server/ip-1@1"}, rowsDepths(crossOrder(rows)))
	})
	t.Run("nests a remote grandchild two levels deep", func(t *testing.T) {
		rows := []listRow{
			{env: localEnvironment, id: "plan-1"},
			{env: "linux-server", id: "ip-2", parent: "ip-1"},
			{env: "linux-server", id: "ip-1", scopeParent: rowKey(localEnvironment, "plan-1")},
		}
		assert.Equal(t, []string{"local/plan-1@0", "linux-server/ip-1@1", "linux-server/ip-2@2"}, rowsDepths(crossOrder(rows)))
	})
	t.Run("prefers the same-environment parent", func(t *testing.T) {
		rows := []listRow{
			{env: localEnvironment, id: "plan-1"},
			{env: "linux-server", id: "ip-1"},
			{env: "linux-server", id: "ip-2", parent: "ip-1", scopeParent: rowKey(localEnvironment, "plan-1")},
		}
		assert.Equal(t, []string{"local/plan-1@0", "linux-server/ip-1@0", "linux-server/ip-2@1"}, rowsDepths(crossOrder(rows)))
	})
	t.Run("keeps a child whose parent isn't listed as a root in place", func(t *testing.T) {
		rows := []listRow{
			{env: localEnvironment, id: "a"},
			{env: "linux-server", id: "c", scopeParent: rowKey(localEnvironment, "missing")},
			{env: localEnvironment, id: "b"},
		}
		assert.Equal(t, []string{"local/a@0", "linux-server/c@0", "local/b@0"}, rowsDepths(crossOrder(rows)))
	})
	t.Run("ends a cycle", func(t *testing.T) {
		rows := []listRow{
			{env: localEnvironment, id: "a", scopeParent: rowKey("linux-server", "b")},
			{env: "linux-server", id: "b", scopeParent: rowKey(localEnvironment, "a")},
		}
		got := crossOrder(rows)
		assert.Equal(t, []string{"local/a@0", "linux-server/b@1"}, rowsDepths(got))
	})
	t.Run("keeps roots in input order", func(t *testing.T) {
		rows := []listRow{
			{env: "linux-server", id: "x"},
			{env: localEnvironment, id: "y"},
		}
		assert.Equal(t, []string{"linux-server/x@0", "local/y@0"}, rowsDepths(crossOrder(rows)))
	})
}
