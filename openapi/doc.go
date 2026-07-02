package openapi

import _ "embed"

// Snapshot is the checked-in Singularity v2 OpenAPI document.
//
//go:embed singularity-v2.json
var Snapshot []byte
