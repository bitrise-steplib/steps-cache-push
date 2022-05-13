package main

import (
	"encoding/json"
	"fmt"
)

func stackVersionData(stackID, architecture string) ([]byte, error) {
	type archiveInfo struct {
		Version     uint64 `json:"version,omitempty"`
		StackID     string `json:"stack_id,omitempty"`
		Arhitecture string `json:"architecture,omitempty"`
	}
	stackData, err := json.Marshal(archiveInfo{
		Version:     2,
		StackID:     stackID,
		Arhitecture: architecture,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data, error: %s", err)
	}
	return stackData, nil
}
