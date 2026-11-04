// Package envfile loads the environment files at start-up, so settings and
// credentials live outside the code and outside config.yaml.
//
// Files, in the order they are read; a later one overrides an earlier one, and
// the real process environment overrides all of them:
//
//	.env         settings, safe to commit
//	.env.local   credentials, git-ignored
//	-env-file F  extra files named on the command line, repeatable
//
// Behaviour:
//   - Lines are KEY=VALUE; "#" starts a comment; blank lines are skipped. There
//     is no interpolation and no "export" prefix, so a file reads the same in
//     every tool.
//   - An empty value counts as unset, so the placeholders in .env.local do not
//     override anything.
//   - Only variables starting with STOXICITY_ or named by a source's auth_env
//     are accepted; others are ignored with a warning.
//   - A .env.local readable by group or others is reported by `doctor`, and the
//     program refuses to use credentials from it until fixed (chmod 600).
//   - Values are never logged, printed or put in error messages. Only names
//     are.
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// keyPattern is the form every variable name must have.
var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Files is a list of extra env files. It satisfies flag.Value, so a flag can be
// given more than once.
type Files []string

// String returns the file names separated by commas.
func (f Files) String() string {
	return strings.Join(f, ",")
}

// Set appends one file name to the list.
func (f *Files) Set(name string) error {
	*f = append(*f, name)
	return nil
}

// Setup reads dir/.env, then dir/.env.local, then each file in extra, and sets
// the accepted variables in the process environment. A later file overrides an
// earlier one. A variable that is already set to a non-empty value in the real
// environment keeps that value, and an empty value is ignored. Only names that
// start with STOXICITY_ or are listed in allow are set; the others are skipped
// with a warning that gives the name. A nil log uses slog.Default. A file whose
// name ends in .local must not be readable by group or others. A missing
// extra file is an error; a missing .env or .env.local is not.
func Setup(extra Files, dir string, allow []string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	inherited := snapshot()

	merged := map[string]string{}
	for _, path := range []string{filepath.Join(dir, ".env"), filepath.Join(dir, ".env.local")} {
		if err := merge(merged, path, true); err != nil {
			return err
		}
	}
	for _, path := range extra {
		if err := merge(merged, path, false); err != nil {
			return err
		}
	}

	for _, key := range slices.Sorted(maps.Keys(merged)) {
		if !accepted(key, allow) {
			log.Warn("envfile: ignoring variable", "name", key)
			continue
		}
		if inherited[key] {
			continue
		}
		if err := os.Setenv(key, merged[key]); err != nil {
			return fmt.Errorf("envfile: set %s: %w", key, err)
		}
	}
	return nil
}

// Parse reads KEY=VALUE lines from r and returns the variables it finds. Blank
// lines and lines that start with "#" are skipped. A value in single or double
// quotes is taken as it is; otherwise a trailing " #comment" is removed. A
// later line with the same key replaces an earlier one. Errors give the line
// number, never the text or value of the line.
func Parse(r io.Reader) (map[string]string, error) {
	vals := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("envfile: line %d: missing '='", n)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("envfile: line %d: empty key", n)
		}
		if !keyPattern.MatchString(key) {
			return nil, fmt.Errorf("envfile: line %d: invalid key", n)
		}
		vals[key] = parseValue(raw)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("envfile: read: %w", err)
	}
	return vals, nil
}

// merge reads the file at path and copies its non-empty values into dst. A
// missing file is not an error when optional is true. A file whose name ends in
// .local is refused when group or others can read it.
func merge(dst map[string]string, path string, optional bool) error {
	info, err := os.Stat(path)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("envfile: %w", err)
	}
	if strings.HasSuffix(filepath.Base(path), ".local") && info.Mode()&0o077 != 0 {
		return fmt.Errorf("envfile: %s is readable by group or others (mode %04o); run chmod 600 %s", path, info.Mode().Perm(), path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("envfile: %w", err)
	}
	defer f.Close()
	vals, err := Parse(f)
	if err != nil {
		return fmt.Errorf("%w in %s", err, path)
	}
	for k, v := range vals {
		if v != "" {
			dst[k] = v
		}
	}
	return nil
}

// snapshot returns the names of the variables that have a non-empty value in
// the process environment.
func snapshot() map[string]bool {
	set := map[string]bool{}
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		if value != "" {
			set[name] = true
		}
	}
	return set
}

// accepted reports whether a file may set the variable name.
func accepted(name string, allow []string) bool {
	return strings.HasPrefix(name, "STOXICITY_") || slices.Contains(allow, name)
}

// parseValue returns the value on a KEY=VALUE line, given the text after "=".
func parseValue(raw string) string {
	v := strings.TrimSpace(raw)
	if s, ok := unquote(v); ok {
		return s
	}
	if i := commentStart(v); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if s, ok := unquote(v); ok {
		return s
	}
	return v
}

// unquote returns the content of v if v is wrapped in matching single or double
// quotes, and reports whether it was.
func unquote(v string) (string, bool) {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1], true
	}
	return "", false
}

// commentStart returns the index of a "#" that starts a trailing comment, which
// is a "#" preceded by a space or tab, or -1 if there is none.
func commentStart(v string) int {
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return i
		}
	}
	return -1
}
