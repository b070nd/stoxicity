package watch

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
	"go.yaml.in/yaml/v3"
)

// FSStore keeps one tenant's watches in one folder, one <id>.yaml file each.
// It implements Store.
type FSStore struct {
	Dir string // the tenant's watches folder
}

var _ Store = FSStore{}

const ext = ".yaml"

// List returns every watch in the folder, sorted by ID. Only files whose names
// end in exactly ".yaml" are read, so ".yaml.example" files are ignored. A
// missing folder holds no watches. Every problem is reported at once, each
// with the name of its file.
func (s FSStore) List() ([]Watch, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("watch: %w", err)
	}
	var ws []Watch
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		w, err := s.read(strings.TrimSuffix(e.Name(), ext))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ws = append(ws, w)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].ID < ws[j].ID })
	return ws, nil
}

// Get returns the watch with the given ID. The ID is checked before the file
// system is touched.
func (s FSStore) Get(id string) (Watch, error) {
	if !model.ValidID(id) {
		return Watch{}, fmt.Errorf("watch: invalid id %q", id)
	}
	w, err := s.read(id)
	if errors.Is(err, fs.ErrNotExist) {
		return Watch{}, fmt.Errorf("watch: unknown watch %q", id)
	}
	return w, err
}

// Save validates w and writes it to <id>.yaml, replacing any earlier file. The
// folder is created if needed.
func (s FSStore) Save(w Watch) error {
	if err := w.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(w)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	if err := writeAtomic(filepath.Join(s.Dir, w.ID+ext), data); err != nil {
		return fmt.Errorf("watch: save %q: %w", w.ID, err)
	}
	return nil
}

// Delete removes the watch with the given ID. It is an error if there is no
// such watch.
func (s FSStore) Delete(id string) error {
	if !model.ValidID(id) {
		return fmt.Errorf("watch: invalid id %q", id)
	}
	err := os.Remove(filepath.Join(s.Dir, id+ext))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("watch: unknown watch %q", id)
	}
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	return nil
}

// read parses <id>.yaml. The watch's own ID must equal id, and the watch must
// be valid. Errors name the file.
func (s FSStore) read(id string) (Watch, error) {
	name := id + ext
	data, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return Watch{}, fmt.Errorf("watch: %w", err)
	}
	var w Watch
	if err := decode(data, &w); err != nil {
		return Watch{}, fmt.Errorf("watch: %s: %w", name, err)
	}
	var errs []error
	if w.ID != id {
		errs = append(errs, fmt.Errorf("watch: %s: id %q must equal the file name", name, w.ID))
	}
	if err := w.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("watch: %s: %w", name, err))
	}
	if err := errors.Join(errs...); err != nil {
		return Watch{}, err
	}
	return w, nil
}

// writeAtomic writes data to path so that a reader sees either the old file
// or the new one. The data goes to a temporary file in the same folder, which
// is synced and then renamed over path. The temporary file is removed if
// anything fails.
func writeAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
