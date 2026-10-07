// Package schemas holds the JSON Schemas for the issue tracker's
// documents. The forms are generated from them at run time by jsonhandler,
// and the Go types in internal/model are generated from them by jsch.
package schemas

import (
	"embed"
	"fmt"
)

//go:generate go tool jsch generate --schema issue.json --out ../internal/model --package model --type Issue
//go:generate go tool jsch generate --schema contact.json --out ../internal/model --package model --type Contact
//go:generate go tool jsch generate --schema settings.json --out ../internal/model --package model --type Settings

//go:embed issue.json contact.json settings.json
var files embed.FS

// Issue, Contact and Settings return the schema documents.
func Issue() []byte    { return mustRead("issue.json") }
func Contact() []byte  { return mustRead("contact.json") }
func Settings() []byte { return mustRead("settings.json") }

// mustRead reads an embedded file. The names are fixed above, so a
// failure means the embed directive and this file disagree.
func mustRead(name string) []byte {
	b, err := files.ReadFile(name)
	if err != nil {
		panic(fmt.Errorf("schemas: %w", err))
	}
	return b
}
