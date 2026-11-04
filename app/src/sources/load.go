package sources

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
	"go.yaml.in/yaml/v3"
)

// Load reads the source registry from the configuration folder dir. It reads
// dir/sources.yaml, which must exist, and then every dir/sources/*.yaml in
// name order. A file in sources/ overrides the entries it names: an entry
// whose id is already known changes only the fields the entry writes, and an
// entry with a new id is added whole. Within one file an id may appear only
// once. Every source is checked, and each problem names the file it came from.
func Load(dir string) (Registry, error) {
	reg := Registry{}
	origin := map[string]string{}

	if err := loadFile(filepath.Join(dir, "sources.yaml"), "sources.yaml", reg, origin); err != nil {
		return nil, err
	}
	// os.ReadDir returns the names in order. A missing sources/ folder is fine.
	entries, err := os.ReadDir(filepath.Join(dir, "sources"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("sources: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := "sources/" + e.Name()
		if err := loadFile(filepath.Join(dir, "sources", e.Name()), name, reg, origin); err != nil {
			return nil, err
		}
	}
	if err := check(reg, origin); err != nil {
		return nil, err
	}
	return reg, nil
}

// loadFile merges the entries of one file into reg. name is the file's name
// for error messages; origin records which file last defined each id.
func loadFile(path, name string, reg Registry, origin map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("sources: %s: %w", name, err)
	}
	var doc struct {
		Sources []model.Source `yaml:"sources"`
	}
	if err := decode(data, &doc); err != nil {
		return fmt.Errorf("sources: %s: %w", name, err)
	}
	// The same entries again, as plain maps, to see which fields each one names.
	var named struct {
		Sources []map[string]any `yaml:"sources"`
	}
	if err := decode(data, &named); err != nil {
		return fmt.Errorf("sources: %s: %w", name, err)
	}
	seen := map[string]bool{}
	for i, s := range doc.Sources {
		if seen[s.ID] {
			return fmt.Errorf("sources: %s: id %q appears twice in one file", name, s.ID)
		}
		seen[s.ID] = true
		if shipped, ok := reg[s.ID]; ok {
			setNamed(&shipped, s, named.Sources[i])
			reg[s.ID] = shipped
		} else {
			reg[s.ID] = s
		}
		origin[s.ID] = name
	}
	return nil
}

// setNamed copies into dst the fields that the entry names. named is the
// entry decoded as a plain map and src is the same entry decoded as a Source.
// Fields are matched by their YAML names.
func setNamed(dst *model.Source, src model.Source, named map[string]any) {
	d := reflect.ValueOf(dst).Elem()
	s := reflect.ValueOf(src)
	t := d.Type()
	for i := range t.NumField() {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if _, ok := named[key]; ok {
			d.Field(i).Set(s.Field(i))
		}
	}
}

// check validates each source on its own, so that every error names the file
// the source came from. The order of the errors is stable.
func check(reg Registry, origin map[string]string) error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(reg)) {
		if err := (Registry{id: reg[id]}).Check(); err != nil {
			errs = append(errs, fmt.Errorf("sources: %s: %w", origin[id], err))
		}
	}
	return errors.Join(errs...)
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
