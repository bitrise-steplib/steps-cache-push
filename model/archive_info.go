package model

import "fmt"

const (
	// Version ...
	Version = 2
)

// ArchiveInfo ...
type ArchiveInfo struct {
	Version      uint64 `json:"version,omitempty"`
	StackID      string `json:"stack_id,omitempty"`
	Architecture string `json:"architecture,omitempty"`
}

// String ...
func (a ArchiveInfo) String() string {
	return fmt.Sprintf("%s (%s)", a.StackID, a.Architecture)
}
