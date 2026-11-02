package modelcatalog

import (
	"fmt"
	"io"
	"os"
)

const modelConfigReadLimit = 2 << 20

func readModelConfig(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // paths are fixed agent configuration locations
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, modelConfigReadLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > modelConfigReadLimit {
		return nil, fmt.Errorf("configuration exceeds %d bytes", modelConfigReadLimit)
	}
	return raw, nil
}
