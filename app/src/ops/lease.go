package ops

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A lease keeps one instance of a job running. It is a file with an owner, an
// expiry and a generation number. The owner renews it while it works; if the
// owner dies the lease runs out and another instance takes over. Taking over
// is decided by creating the file of the next generation exclusively, so two
// instances that both see an expired lease cannot both win.
//
// A lease is advisory: it protects against two copies of this program doing the
// same job, not against a program that ignores it.

// ErrHeld is returned when somebody else holds the lease.
type ErrHeld struct {
	Name   string
	Holder string
	Until  time.Time
}

func (e *ErrHeld) Error() string {
	return fmt.Sprintf("ops: %s is held by %s until %s", e.Name, e.Holder, e.Until.UTC().Format(time.RFC3339))
}

// ErrLost is returned by Renew when the lease was taken over.
var ErrLost = errors.New("ops: the lease was taken over by another instance")

var leaseNameRE = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,60}$`)

type leaseFile struct {
	Name     string    `json:"name"`
	Holder   string    `json:"holder"`
	Gen      int64     `json:"generation"`
	Acquired time.Time `json:"acquired"`
	Expires  time.Time `json:"expires"`
}

// Leases is a folder of leases.
type Leases struct {
	Dir string
	Now func() time.Time
}

func (l Leases) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

func (l Leases) path(name string) string { return filepath.Join(l.Dir, name+".lease") }

func (l Leases) read(name string) (leaseFile, bool, error) {
	b, err := os.ReadFile(l.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return leaseFile{}, false, nil
	}
	if err != nil {
		return leaseFile{}, false, err
	}
	var f leaseFile
	if err := json.Unmarshal(b, &f); err != nil {
		return leaseFile{}, false, fmt.Errorf("ops: lease %s is damaged: %w", name, err)
	}
	return f, true, nil
}

func (l Leases) write(f leaseFile) error {
	b, _ := json.Marshal(f)
	tmp := l.path(f.Name) + ".tmp." + strconv.FormatInt(f.Gen, 10) + "." + randHex(4)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.path(f.Name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Held is a lease this instance owns.
type Held struct {
	l    Leases
	name string
	gen  int64
	who  string
}

// Acquire takes the lease for ttl, or says who holds it. holder names this
// instance in the lease file; it is a label for people, not a secret.
func (l Leases) Acquire(name, holder string, ttl time.Duration) (*Held, error) {
	if !leaseNameRE.MatchString(name) {
		return nil, fmt.Errorf("ops: %q is not a lease name", name)
	}
	if ttl <= 0 {
		return nil, errors.New("ops: a lease needs a positive time to live")
	}
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return nil, err
	}
	skip := int64(0)
	for attempt := 0; attempt < 6; attempt++ {
		cur, ok, err := l.read(name)
		if err != nil {
			return nil, err
		}
		now := l.now()
		if ok && now.Before(cur.Expires) {
			return nil, &ErrHeld{Name: name, Holder: cur.Holder, Until: cur.Expires}
		}
		gen := cur.Gen + 1 + skip
		marker := filepath.Join(l.Dir, fmt.Sprintf("%s.gen%d", name, gen))
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			// Either somebody just won this generation and is writing the lease,
			// or the winner died before it wrote. A marker that has been there
			// for a while and has no lease behind it is the second case.
			if st, serr := os.Stat(marker); serr == nil && time.Since(st.ModTime()) > 5*time.Second {
				skip++
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		f.Close()
		if err := l.write(leaseFile{Name: name, Holder: holder, Gen: gen, Acquired: now, Expires: now.Add(ttl)}); err != nil {
			return nil, err
		}
		if back, ok, err := l.read(name); err != nil || !ok || back.Gen != gen {
			return nil, &ErrHeld{Name: name, Holder: back.Holder, Until: back.Expires}
		}
		l.sweep(name, gen)
		return &Held{l: l, name: name, gen: gen, who: holder}, nil
	}
	cur, _, _ := l.read(name)
	return nil, &ErrHeld{Name: name, Holder: cur.Holder, Until: cur.Expires}
}

// sweep removes the markers of generations that are over.
func (l Leases) sweep(name string, gen int64) {
	ents, _ := os.ReadDir(l.Dir)
	for _, e := range ents {
		n := e.Name()
		if rest, ok := strings.CutPrefix(n, name+".gen"); ok {
			if g, err := strconv.ParseInt(rest, 10, 64); err == nil && g < gen {
				os.Remove(filepath.Join(l.Dir, n))
			}
		}
	}
}

// Renew extends the lease by ttl from now. It fails with ErrLost when another
// instance took the lease over, and the work must then stop.
func (h *Held) Renew(ttl time.Duration) error {
	cur, ok, err := h.l.read(h.name)
	if err != nil {
		return err
	}
	if !ok || cur.Gen != h.gen {
		return ErrLost
	}
	cur.Expires = h.l.now().Add(ttl)
	return h.l.write(cur)
}

// Valid reports whether this instance still owns an unexpired lease.
func (h *Held) Valid() bool {
	cur, ok, err := h.l.read(h.name)
	return err == nil && ok && cur.Gen == h.gen && h.l.now().Before(cur.Expires)
}

// Generation is the fencing number of this lease: a later owner has a larger one.
func (h *Held) Generation() int64 { return h.gen }

// Release gives the lease up if it is still ours.
func (h *Held) Release() error {
	cur, ok, err := h.l.read(h.name)
	if err != nil || !ok || cur.Gen != h.gen {
		return err
	}
	cur.Expires = h.l.now().Add(-time.Second)
	return h.l.write(cur)
}

// Info is a lease as listed.
type Info struct {
	Name    string    `json:"name"`
	Holder  string    `json:"holder"`
	Gen     int64     `json:"generation"`
	Expires time.Time `json:"expires"`
	Active  bool      `json:"active"`
}

// List returns every lease, by name.
func (l Leases) List() ([]Info, error) {
	ents, err := os.ReadDir(l.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".lease")
		if !ok {
			continue
		}
		f, ok, err := l.read(name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, Info{Name: name, Holder: f.Holder, Gen: f.Gen, Expires: f.Expires, Active: l.now().Before(f.Expires)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
