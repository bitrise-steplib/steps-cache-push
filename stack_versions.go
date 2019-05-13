package main

import (
	"encoding/json"
	"fmt"
)

func stackVersions(stackID string) ([]byte, error) {
	type archiveInfo struct {
		StackID string `json:"stack_id,omitempty"`
	}
	stackData, err := json.Marshal(archiveInfo{
		StackID: stackID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data, error: %s", err)
	}
	return stackData, nil
}
