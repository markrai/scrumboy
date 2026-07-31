package todo

import "fmt"

type MCPMoveValidationKind string

const (
	MCPMoveMissingColumn            MCPMoveValidationKind = "missing_column"
	MCPMoveInvalidLocalReference    MCPMoveValidationKind = "invalid_local_reference"
	MCPMoveReferenceInWrongColumn   MCPMoveValidationKind = "reference_in_wrong_column"
	MCPMoveAmbiguousAfterReference  MCPMoveValidationKind = "ambiguous_after_reference"
	MCPMoveAmbiguousBeforeReference MCPMoveValidationKind = "ambiguous_before_reference"
	MCPMoveInvalidNeighbor          MCPMoveValidationKind = "invalid_neighbor"
)

// MCPMoveValidationError identifies MCP-only neighbor and boundary policy
// failures without coupling the application package to MCP envelopes.
type MCPMoveValidationError struct {
	Kind       MCPMoveValidationKind
	Field      string
	LocalID    int64
	HasLocalID bool
}

func (e *MCPMoveValidationError) Error() string {
	return fmt.Sprintf("MCP todo move validation failed: %s", e.Kind)
}

// MCPMoveAnchorReadError preserves the adapter's historical generic mapping
// for failures from the lane-boundary read, distinct from privileged todo
// lookup and mutation failures.
type MCPMoveAnchorReadError struct {
	Err error
}

func (e *MCPMoveAnchorReadError) Error() string {
	return fmt.Sprintf("MCP todo move anchor read failed: %v", e.Err)
}

func (e *MCPMoveAnchorReadError) Unwrap() error {
	return e.Err
}
