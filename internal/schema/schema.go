// Package schema holds the authoritative PocketBase collections snapshot.
//
// The snapshot is applied in full on every start: declared collections and
// fields are created or restored, undeclared ones are deleted with their
// records (PocketBase system collections excepted). Never change the schema
// in the admin UI; edit collections.json and commit it.
package schema

import (
	_ "embed"
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

//go:embed collections.json
var snapshot []byte

// Snapshot returns the embedded collections JSON.
func Snapshot() []byte { return snapshot }

// Apply imports the snapshot, deleting anything it doesn't declare.
func Apply(app core.App) error {
	if err := app.ImportCollectionsByMarshaledJSON(snapshot, true); err != nil {
		return fmt.Errorf("schema: apply snapshot: %w", err)
	}
	return nil
}

// Collection names, shared by the rest of Nexus.
const (
	Users           = "users"
	AppPasswords    = "app_passwords"
	Calendars       = "calendars"
	CalendarObjects = "calendar_objects"
	Addressbooks    = "addressbooks"
	Contacts        = "contacts"
	DAVChanges      = "dav_changes"
	StorageAccounts = "storage_accounts"
	Mounts          = "mounts"
)
