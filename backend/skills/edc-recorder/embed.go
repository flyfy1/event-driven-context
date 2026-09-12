package edcrecorder

import _ "embed"

// Content is the versioned recorder skill installed by edc setup.
//
//go:embed SKILL.md
var Content []byte
