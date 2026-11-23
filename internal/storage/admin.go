package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/rclone/rclone/fs"
)

// What the admin dashboard needs from storage: the options a backend takes,
// an account's stored secrets, and folder listings for the connection test
// and the remote path picker.

// OptionInfo describes one backend option for a settings form.
type OptionInfo struct {
	Name      string          `json:"name"`
	Type      string          `json:"type" doc:"rclone's value type: string, bool, int, SizeSuffix, Duration, …"`
	Help      string          `json:"help"`
	Secret    bool            `json:"secret" doc:"Stored encrypted and never shown again"`
	Advanced  bool            `json:"advanced"`
	Required  bool            `json:"required"`
	Exclusive bool            `json:"exclusive" doc:"Only the examples are allowed"`
	Default   string          `json:"default"`
	Provider  string          `json:"provider" doc:"Providers it applies to (rclone's syntax: a,b or !a,b); empty means all"`
	Examples  []OptionExample `json:"examples"`
}

// OptionExample is a suggested value.
type OptionExample struct {
	Value    string `json:"value"`
	Help     string `json:"help"`
	Provider string `json:"provider"`
}

// Options lists what a backend type takes, in rclone's order.
func Options(typ string) ([]OptionInfo, error) {
	ri, err := fs.Find(typ)
	if err != nil {
		return nil, fmt.Errorf("unknown storage type %q", typ)
	}
	out := make([]OptionInfo, 0, len(ri.Options))
	for _, o := range ri.Options {
		if o.Hide&fs.OptionHideConfigurator != 0 {
			continue
		}
		info := OptionInfo{
			Name:      o.Name,
			Type:      o.Type(),
			Help:      o.Help,
			Secret:    isSecret(&o),
			Advanced:  o.Advanced,
			Required:  o.Required,
			Exclusive: o.Exclusive,
			Provider:  o.Provider,
			Examples:  make([]OptionExample, 0, len(o.Examples)),
		}
		if o.Default != nil {
			info.Default = fmt.Sprint(o.Default)
		}
		for _, e := range o.Examples {
			info.Examples = append(info.Examples, OptionExample{e.Value, e.Help, e.Provider})
		}
		out = append(out, info)
	}
	return out, nil
}

// Secrets returns an account's decrypted secret options. They are for the
// server only (merging an update); the API returns their names.
func (m *Manager) Secrets(acc *core.Record) (map[string]string, error) {
	sealed := acc.GetString("secrets_enc")
	if sealed == "" {
		return map[string]string{}, nil
	}
	b, err := m.sealer.Open(sealed, acc.Id)
	if err != nil {
		return nil, err
	}
	var secrets map[string]string
	if err := json.Unmarshal(b, &secrets); err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	return secrets, nil
}

// Entry is one item in a folder listing.
type Entry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified,omitzero"`
}

// MaxBrowseEntries bounds one listing.
const MaxBrowseEntries = 1000

// Browse errors.
var (
	ErrBrowseNotFound = errors.New("no such folder")
	ErrBrowseNotDir   = errors.New("that is a file, not a folder")
)

// Browse lists a folder of a storage account: p is a remote path as a mount
// would use it ("/srv/data" for local disk, "bucket/prefix" for S3, "" for
// the list of buckets). Folders come first. The second result says the
// listing was cut at MaxBrowseEntries.
func (m *Manager) Browse(ctx context.Context, acc *core.Record, p string) ([]Entry, bool, error) {
	if err := checkRemotePath(acc.GetString("type"), p); err != nil && p != "" {
		return nil, false, err
	}
	cm, err := m.config(acc)
	if err != nil {
		return nil, false, err
	}
	ri, err := fs.Find(acc.GetString("type"))
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// A connection test should say what's wrong at once, not retry a
	// refused connection or a bad key until the deadline hides the cause.
	ctx, ci := fs.AddConfig(ctx)
	ci.LowLevelRetries = 1
	f, err := ri.NewFs(ctx, "nexus-browse-"+acc.Id, p, cm)
	if errors.Is(err, fs.ErrorIsFile) {
		return nil, false, ErrBrowseNotDir
	}
	if err != nil {
		return nil, false, err
	}
	list, err := f.List(ctx, "")
	if errors.Is(err, fs.ErrorDirNotFound) {
		return nil, false, ErrBrowseNotFound
	}
	if err != nil {
		return nil, false, err
	}
	out := make([]Entry, 0, min(len(list), MaxBrowseEntries))
	for _, e := range list {
		en := Entry{Name: path.Base(e.Remote()), Modified: e.ModTime(ctx)}
		if _, ok := e.(fs.Directory); ok {
			en.Dir = true
		} else {
			en.Size = max(e.Size(), 0)
		}
		if en.Modified.Unix() <= 0 {
			en.Modified = time.Time{}
		}
		out = append(out, en)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	truncated := len(out) > MaxBrowseEntries
	if truncated {
		out = out[:MaxBrowseEntries]
	}
	return out, truncated, nil
}
