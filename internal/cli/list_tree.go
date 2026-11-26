package cli

// rowKey names a table row by its environment and workspace id.
func rowKey(env, id string) string {
	return env + "/" + id
}

// crossOrder orders rows as one tree across environments.
func crossOrder(rows []listRow) []listRow {
	return rows
}
