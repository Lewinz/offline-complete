// Package shellintegration contains the zsh hook installed for new shells.
package shellintegration

import _ "embed"

// Script is copied to the user's data directory by oc-manage shell install.
// The @OC@ placeholder is replaced with the quoted oc executable path.
//
//go:embed shell-integration.zsh
var Script string
