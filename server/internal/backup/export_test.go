package backup

// SetRename replaces os.Rename in swaps and publishing until the test ends.
func SetRename(t interface{ Cleanup(func()) }, f func(from, to string) error) {
	old := rename
	rename = f
	t.Cleanup(func() { rename = old })
}

// SetLink replaces os.Link in publishing until the test ends.
func SetLink(t interface{ Cleanup(func()) }, f func(from, to string) error) {
	old := link
	link = f
	t.Cleanup(func() { link = old })
}
