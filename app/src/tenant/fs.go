package tenant

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/b070nd/stoxicity/app/src/model"
	"go.yaml.in/yaml/v3"
)

// FSRegistry finds tenants in a folder on disk: one subfolder per tenant, each
// holding a tenant.yaml. It implements Registry.
type FSRegistry struct {
	Dir string // the tenants folder, for example config/tenants
}

var _ Registry = FSRegistry{}

// List returns every tenant, sorted by ID. A subfolder whose name is not a
// valid ID, or that has no tenant.yaml, is skipped. A tenant whose tenant.yaml
// names a different ID than its folder is an error.
func (r FSRegistry) List() ([]Tenant, error) {
	// os.ReadDir returns the names in order, and a tenant's ID is its folder name.
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, fmt.Errorf("tenant: %w", err)
	}
	var out []Tenant
	for _, e := range entries {
		if !e.IsDir() || !model.ValidID(e.Name()) {
			continue
		}
		t, err := r.read(e.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// Get returns the tenant with the given ID. The ID is checked before the file
// system is touched, so a path such as "../x" is rejected.
func (r FSRegistry) Get(id string) (Tenant, error) {
	if !model.ValidID(id) {
		return Tenant{}, fmt.Errorf("tenant: invalid id %q", id)
	}
	t, err := r.read(id)
	if errors.Is(err, fs.ErrNotExist) {
		return Tenant{}, fmt.Errorf("tenant: unknown tenant %q", id)
	}
	return t, err
}

// read parses the tenant.yaml of the tenant whose folder is id. The caller
// must have checked that id is a valid ID.
func (r FSRegistry) read(id string) (Tenant, error) {
	dir := filepath.Join(r.Dir, id)
	data, err := os.ReadFile(filepath.Join(dir, "tenant.yaml"))
	if err != nil {
		return Tenant{}, fmt.Errorf("tenant: %w", err)
	}
	var t Tenant
	if err := decode(data, &t); err != nil {
		return Tenant{}, fmt.Errorf("tenant: %s/tenant.yaml: %w", id, err)
	}
	if t.ID != id {
		return Tenant{}, fmt.Errorf("tenant: %s/tenant.yaml: id %q must equal the folder name %q", id, t.ID, id)
	}
	t.Dir = dir
	return t, nil
}

// decode reads one YAML document into v and rejects keys that v does not
// have. An empty document leaves v unchanged.
func decode(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
