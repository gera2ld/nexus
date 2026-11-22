package storage

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	validation "ganigeorgiev.com/pocketbase/ozzo-validation/v4"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/rclone/rclone/fs"

	"nexus/internal/schema"
)

// isSecret reports whether rclone treats an option as a secret.
func isSecret(o *fs.Option) bool { return o.IsPassword || o.Sensitive }

func backendOptions(typ string) (map[string]*fs.Option, error) {
	ri, err := fs.Find(typ)
	if err != nil {
		return nil, fmt.Errorf("unknown storage type %q", typ)
	}
	out := make(map[string]*fs.Option, len(ri.Options))
	for i := range ri.Options {
		out[ri.Options[i].Name] = &ri.Options[i]
	}
	return out, nil
}

// decodeObject reads a JSON field as an object of option values. Blank and
// null are an empty map.
func decodeObject(raw []byte) (map[string]string, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == `""` {
		return map[string]string{}, nil
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		return nil, fmt.Errorf("must be a JSON object of option names to values")
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch x := v.(type) {
		case string:
			out[k] = x
		case bool:
			out[k] = strconv.FormatBool(x)
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		default:
			return nil, fmt.Errorf("option %q must be a string, number or boolean", k)
		}
	}
	return out, nil
}

// checkKeys validates option names for a backend. secret selects which kind
// of option the map may hold.
func checkKeys(opts map[string]*fs.Option, values map[string]string, secret bool) error {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o, ok := opts[k]
		switch {
		case !ok:
			return fmt.Errorf("unknown option %q", k)
		case secret && !isSecret(o):
			return fmt.Errorf("%q is not a secret; put it in options", k)
		case !secret && isSecret(o):
			return fmt.Errorf("%q is a secret; put it in secret_options so it is stored encrypted", k)
		}
	}
	return nil
}

// onAccountSave validates options and moves secret_options into
// secrets_enc. It runs before the record is written.
func (m *Manager) onAccountSave(e *core.RecordEvent) error {
	r := e.Record
	typ := r.GetString("type")
	opts, err := backendOptions(typ)
	if err != nil {
		return validation.Errors{"type": validation.NewError("nexus_storage_type", err.Error())}
	}
	plain, err := decodeObject(jsonBytes(r.Get("options")))
	if err == nil {
		err = checkKeys(opts, plain, false)
	}
	if err != nil {
		return validation.Errors{"options": validation.NewError("nexus_storage_options", err.Error())}
	}

	if r.Id == "" {
		r.Id = core.GenerateDefaultRandomId() // secrets are bound to the id
	}
	secrets, err := decodeObject(jsonBytes(r.Get("secret_options")))
	if err == nil {
		err = checkKeys(opts, secrets, true)
	}
	if err != nil {
		return validation.Errors{"secret_options": validation.NewError("nexus_storage_secrets", err.Error())}
	}
	switch {
	case len(secrets) > 0:
		b, _ := json.Marshal(secrets)
		sealed, err := m.sealer.Seal(b, r.Id)
		if err != nil {
			return validation.Errors{"secret_options": validation.NewError("nexus_storage_secrets", err.Error())}
		}
		r.Set("secrets_enc", sealed)
	case !r.IsNew() && r.Original().GetString("type") != typ && r.GetString("secrets_enc") != "":
		return validation.Errors{"secret_options": validation.NewError("nexus_storage_secrets",
			"the storage type changed; enter the secrets for the new type")}
	}
	r.Set("secret_options", nil) // write-only: never stored in plain text
	return e.Next()
}

// onMountSave checks remote_path against the account's backend.
func (m *Manager) onMountSave(e *core.RecordEvent) error {
	r := e.Record
	acc, err := e.App.FindRecordById(schema.StorageAccounts, r.GetString("account"))
	if err != nil {
		return validation.Errors{"account": validation.NewError("nexus_mount_account", "storage account not found")}
	}
	p := r.GetString("remote_path")
	if err := checkRemotePath(acc.GetString("type"), p); err != nil {
		return validation.Errors{"remote_path": validation.NewError("nexus_mount_path", err.Error())}
	}
	return e.Next()
}

func checkRemotePath(typ, p string) error {
	for _, el := range strings.Split(filepath.ToSlash(p), "/") {
		if el == ".." {
			return fmt.Errorf("must not contain ..")
		}
	}
	switch typ {
	case "local":
		if !filepath.IsAbs(p) {
			return fmt.Errorf("must be an absolute directory, like /srv/nexus/ada")
		}
	case "s3":
		if strings.HasPrefix(p, "/") || path.Clean(p) == "." {
			return fmt.Errorf("must be bucket or bucket/prefix, with no leading /")
		}
	}
	return nil
}

func jsonBytes(v any) []byte {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		return x
	case string:
		return []byte(x)
	case fmt.Stringer:
		return []byte(x.String())
	}
	b, _ := json.Marshal(v)
	return b
}
