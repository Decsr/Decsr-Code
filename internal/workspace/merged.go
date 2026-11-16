package workspace

// HasMergedNode reports whether at least one node of the session was merged
// into the session branch, that is, whether the session changed anything.
func HasMergedNode(root, session string) bool { return hasMergedNode(root, session) }
