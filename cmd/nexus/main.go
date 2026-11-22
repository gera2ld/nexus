// Command nexus is the Nexus personal DAV server.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/migratecmd"

	"nexus/internal/nexus"
)

func main() {
	app := pocketbase.New()

	// The schema is an embedded snapshot (internal/schema), not migrations.
	// migratecmd is kept only for `migrate down` and one-off data fixes;
	// automigrate stays off because it writes a migration per UI edit.
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{Automigrate: false})

	nexus.Setup(app)

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
