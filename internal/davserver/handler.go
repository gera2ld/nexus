// Package davserver routes /dav requests to the principal, CalDAV and
// CardDAV handlers. Authentication happens before it; it only enforces that
// a user stays inside their own /dav/{uid}/ tree.
package davserver

import (
	"net/http"
	"path"
	"strings"

	webdav "nexus/internal/dav"
	"nexus/internal/dav/caldav"
	"nexus/internal/dav/carddav"
	"nexus/internal/principal"
	"nexus/internal/store"
)

// New returns the /dav handler for st.
func New(st *store.Store) http.Handler {
	cal := &caldav.Handler{Backend: st.CalDAV(), Prefix: store.Prefix}
	card := &carddav.Handler{Backend: st.CardDAV(), Prefix: store.Prefix}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := principal.ID(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.Path, "/../") || strings.HasSuffix(r.URL.Path, "/..") {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}

		rest := strings.TrimPrefix(path.Clean(r.URL.Path), store.Prefix)
		segs := strings.Split(strings.Trim(rest, "/"), "/")
		if segs[0] != "" && segs[0] != uid {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		switch {
		case len(segs) >= 2 && segs[1] == "calendars":
			cal.ServeHTTP(w, r)
		case len(segs) >= 2 && segs[1] == "contacts":
			card.ServeHTTP(w, r)
		case len(segs) == 1: // /dav/ or /dav/{uid}/
			webdav.ServePrincipal(w, r, &webdav.ServePrincipalOptions{
				CurrentUserPrincipalPath: store.Prefix + "/" + uid + "/",
				HomeSets: []webdav.BackendSuppliedHomeSet{
					caldav.NewCalendarHomeSet(store.Prefix + "/" + uid + "/calendars/"),
					carddav.NewAddressBookHomeSet(store.Prefix + "/" + uid + "/contacts/"),
				},
				Capabilities: []webdav.Capability{caldav.CapabilityCalendar, carddav.CapabilityAddressBook},
			})
		default:
			http.NotFound(w, r)
		}
	})
}
