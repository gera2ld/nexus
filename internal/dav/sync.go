package webdav

import "errors"

// ErrInvalidSyncToken is returned by a sync backend when the client's token
// is unknown or expired (e.g. its tombstones were pruned). The handler turns
// it into 403 DAV:valid-sync-token so the client resyncs from scratch.
var ErrInvalidSyncToken = errors.New("webdav: invalid sync token")
