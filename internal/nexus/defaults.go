package nexus

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"nexus/internal/schema"
)

// Default collections every new user gets.
const (
	DefaultCalendarSlug    = "personal"
	DefaultAddressbookSlug = "contacts"
)

// createDefaultCollections runs as the users create-execute hook. It wraps
// the user INSERT and the default calendar and address book in one
// transaction, so a user never exists without them.
func createDefaultCollections(e *core.RecordEvent) error {
	return e.App.RunInTransaction(func(tx core.App) error {
		e.App = tx
		if err := e.Next(); err != nil {
			return err
		}
		return ensureDefaults(tx, e.Record.Id)
	})
}

func ensureDefaults(app core.App, userID string) error {
	cals, err := app.FindCollectionByNameOrId(schema.Calendars)
	if err != nil {
		return err
	}
	cal := core.NewRecord(cals)
	cal.Set("owner", userID)
	cal.Set("slug", DefaultCalendarSlug)
	cal.Set("name", "Personal")
	cal.Set("components", []string{"VEVENT", "VTODO"})
	if err := app.Save(cal); err != nil {
		return fmt.Errorf("nexus: create default calendar: %w", err)
	}

	books, err := app.FindCollectionByNameOrId(schema.Addressbooks)
	if err != nil {
		return err
	}
	book := core.NewRecord(books)
	book.Set("owner", userID)
	book.Set("slug", DefaultAddressbookSlug)
	book.Set("name", "Contacts")
	if err := app.Save(book); err != nil {
		return fmt.Errorf("nexus: create default address book: %w", err)
	}
	return nil
}
