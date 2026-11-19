package runner

import (
	json "encoding/json/v2"
	"fmt"
	"os"
)

// UsageName is usage.json's file name, next to a child's result.
const UsageName = usageFile

// ReadUsage reads the usage.json at path.
func ReadUsage(path string) (Usage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Usage{}, fmt.Errorf("reading usage.json: %w", err)
	}
	var usage Usage
	if err := json.Unmarshal(data, &usage); err != nil {
		return Usage{}, fmt.Errorf("parsing usage.json: %w", err)
	}
	return usage, nil
}
