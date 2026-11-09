package vm

func newMemStateView() StateView {
	read := func(key string) ([]byte, error) { return nil, nil }
	scan := func(prefix string) (map[string][]byte, error) { return nil, nil }
	return NewStateView(read, scan)
}
