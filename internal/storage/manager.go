// Package storage maps a user's mounts to rclone VFS instances
// (docs/ARCHITECTURE.md §7). Only the admin defines storage accounts and
// mounts; their secrets are encrypted at rest with NEXUS_ENC_KEY.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "github.com/rclone/rclone/backend/local" // the backends Nexus offers
	_ "github.com/rclone/rclone/backend/s3"
	_ "github.com/rclone/rclone/backend/webdav"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/vfs"
	"github.com/rclone/rclone/vfs/vfscommon"

	"nexus/internal/schema"
)

// DirCacheTime is how long directory listings are cached, so changes made
// outside Nexus show up within this time.
const DirCacheTime = time.Minute

// ErrNoMount is returned when the user has no mount by that name.
var ErrNoMount = errors.New("storage: no such mount")

// Mount is an opened mount.
type Mount struct {
	ID       string
	Name     string
	ReadOnly bool
	VFS      *vfs.VFS

	account string
}

// Manager opens and caches mounts.
type Manager struct {
	app    core.App
	sealer *Sealer

	mu    sync.Mutex
	cache map[string]*Mount // by mount id
}

// NewManager returns a Manager; sealer may be nil if no key is configured.
func NewManager(app core.App, sealer *Sealer) *Manager {
	return &Manager{app: app, sealer: sealer, cache: map[string]*Mount{}}
}

// Bind registers the save checks and cache invalidation hooks.
func (m *Manager) Bind(app core.App) {
	app.OnRecordCreate(schema.StorageAccounts).BindFunc(m.onAccountSave)
	app.OnRecordUpdate(schema.StorageAccounts).BindFunc(m.onAccountSave)
	app.OnRecordCreate(schema.Mounts).BindFunc(m.onMountSave)
	app.OnRecordUpdate(schema.Mounts).BindFunc(m.onMountSave)

	dropMount := func(e *core.RecordEvent) error {
		m.drop(func(mt *Mount) bool { return mt.ID == e.Record.Id })
		return e.Next()
	}
	dropAccount := func(e *core.RecordEvent) error {
		m.drop(func(mt *Mount) bool { return mt.account == e.Record.Id })
		return e.Next()
	}
	app.OnRecordAfterUpdateSuccess(schema.Mounts).BindFunc(dropMount)
	app.OnRecordAfterDeleteSuccess(schema.Mounts).BindFunc(dropMount)
	app.OnRecordAfterUpdateSuccess(schema.StorageAccounts).BindFunc(dropAccount)
	app.OnRecordAfterDeleteSuccess(schema.StorageAccounts).BindFunc(dropAccount)
}

// List returns the user's mount records, by name.
func (m *Manager) List(userID string) ([]*core.Record, error) {
	return m.app.FindRecordsByFilter(schema.Mounts, "user = {:u}", "name", 0, 0, dbx.Params{"u": userID})
}

// Get opens the user's mount by name, reusing a cached VFS.
func (m *Manager) Get(ctx context.Context, userID, name string) (*Mount, error) {
	rec, err := m.app.FindFirstRecordByFilter(schema.Mounts, "user = {:u} && name = {:n}",
		dbx.Params{"u": userID, "n": name})
	if err != nil {
		return nil, ErrNoMount
	}
	m.mu.Lock()
	mt := m.cache[rec.Id]
	m.mu.Unlock()
	if mt != nil {
		return mt, nil
	}

	mt, err = m.open(ctx, rec)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if other := m.cache[rec.Id]; other != nil { // lost a race; keep the first
		mt.VFS.Shutdown()
		return other, nil
	}
	m.cache[rec.Id] = mt
	return mt, nil
}

func (m *Manager) open(ctx context.Context, rec *core.Record) (*Mount, error) {
	acc, err := m.app.FindRecordById(schema.StorageAccounts, rec.GetString("account"))
	if err != nil {
		return nil, fmt.Errorf("storage: mount %s: account: %w", rec.Id, err)
	}
	cm, err := m.config(acc)
	if err != nil {
		return nil, fmt.Errorf("storage: mount %s: %w", rec.Id, err)
	}
	ri, err := fs.Find(acc.GetString("type"))
	if err != nil {
		return nil, fmt.Errorf("storage: mount %s: %w", rec.Id, err)
	}
	// A distinct name per mount keeps rclone's VFS registry from sharing
	// state between mounts.
	f, err := ri.NewFs(ctx, "nexus-"+rec.Id, rec.GetString("remote_path"), cm)
	if errors.Is(err, fs.ErrorIsFile) {
		return nil, fmt.Errorf("storage: mount %s: remote_path is a file, not a directory", rec.Id)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: mount %s: %w", rec.Id, err)
	}

	opt := vfscommon.Opt
	opt.CacheMode = vfscommon.CacheModeOff // stream writes straight through
	opt.DirCacheTime = fs.Duration(DirCacheTime)
	opt.ReadOnly = rec.GetBool("read_only")
	return &Mount{
		ID:       rec.Id,
		Name:     rec.GetString("name"),
		ReadOnly: rec.GetBool("read_only"),
		VFS:      vfs.New(context.Background(), f, &opt),
		account:  acc.Id,
	}, nil
}

// optionDefaults gives rclone a backend's default for every option the
// account doesn't set. Backends read their whole config from the map, so
// without it every unset option is zero (S3 refuses a chunk size of 0).
// Unlike rclone's own fs.ConfigMap, it doesn't read RCLONE_* environment
// variables or a config file: an account's settings are all in Nexus.
type optionDefaults map[string]*fs.Option

func (d optionDefaults) Get(key string) (string, bool) {
	o := d[key]
	if o == nil {
		return "", false
	}
	return o.String(), true
}

// config merges an account's options and decrypted secrets for rclone, over
// the backend's defaults.
func (m *Manager) config(acc *core.Record) (configmap.Mapper, error) {
	opts, err := backendOptions(acc.GetString("type"))
	if err != nil {
		return nil, err
	}
	cm := configmap.Simple{}
	plain, err := decodeObject(jsonBytes(acc.Get("options")))
	if err != nil {
		return nil, fmt.Errorf("options: %w", err)
	}
	for k, v := range plain {
		cm[k] = v
	}
	if sealed := acc.GetString("secrets_enc"); sealed != "" {
		b, err := m.sealer.Open(sealed, acc.Id)
		if err != nil {
			return nil, err
		}
		var secrets map[string]string
		if err := json.Unmarshal(b, &secrets); err != nil {
			return nil, fmt.Errorf("secrets: %w", err)
		}
		for k, v := range secrets {
			if o := opts[k]; o != nil && o.IsPassword {
				v = obscure.MustObscure(v) // rclone expects these obscured
			}
			cm[k] = v
		}
	}
	out := configmap.New()
	out.AddGetter(cm, configmap.PriorityNormal)
	out.AddGetter(optionDefaults(opts), configmap.PriorityDefault)
	return out, nil
}

func (m *Manager) drop(match func(*Mount) bool) {
	m.mu.Lock()
	var gone []*Mount
	for id, mt := range m.cache {
		if match(mt) {
			gone = append(gone, mt)
			delete(m.cache, id)
		}
	}
	m.mu.Unlock()
	for _, mt := range gone {
		mt.VFS.Shutdown()
	}
}

// Close shuts every cached VFS down.
func (m *Manager) Close() { m.drop(func(*Mount) bool { return true }) }
