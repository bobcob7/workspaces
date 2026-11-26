package cli

// rowKey names a table row by its environment and workspace id.
func rowKey(env, id string) string {
	return env + "/" + id
}

// crossOrder orders rows as one tree across environments: a row's parent is
// its same-environment parent when listed, else its scope parent when listed.
// Other rows are roots in input order, and depths are recomputed.
func crossOrder(rows []listRow) []listRow {
	keys := make(map[string]bool, len(rows))
	for i := range rows {
		keys[rowKey(rows[i].env, rows[i].id)] = true
	}
	order, depths := orderTree(len(rows), func(i int) (string, string) {
		return rowKey(rows[i].env, rows[i].id), listedParentKey(&rows[i], keys)
	})
	out := make([]listRow, 0, len(order))
	for k, i := range order {
		row := rows[i]
		row.depth = depths[k]
		out = append(out, row)
	}
	return out
}

// listedParentKey is the key of r's parent when that row is listed, else "".
func listedParentKey(r *listRow, keys map[string]bool) string {
	if r.parent != "" && keys[rowKey(r.env, r.parent)] {
		return rowKey(r.env, r.parent)
	}
	if keys[r.scopeParent] {
		return r.scopeParent
	}
	return ""
}
