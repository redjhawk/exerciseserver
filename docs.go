// Package exerciseserver embeds the project documents that the server publishes,
// so they always match the deployed version.
package exerciseserver

import _ "embed"

// InstruccionesClaude is INSTRUCCIONES_CLAUDE.md: how an AI must build and publish exercises.
//
//go:embed INSTRUCCIONES_CLAUDE.md
var InstruccionesClaude string
