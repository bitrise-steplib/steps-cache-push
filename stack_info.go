package main

import (
	"encoding/json"
	"fmt"

	"github.com/bitrise-steplib/steps-cache-push/model"
)

func stackVersionData(stackID, architecture string) ([]byte, error) {
	stackData, err := json.Marshal(model.ArchiveInfo{
		Version:      model.Version,
		StackID:      stackID,
		Architecture: architecture,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data, error: %s", err)
	}
	return stackData, nil
}
