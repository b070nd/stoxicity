package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileChannel writes each notification as one Markdown file in a folder.
type FileChannel struct {
	dir string
}

var _ Channel = (*FileChannel)(nil)

// NewFile returns a channel that writes into dir. The folder is created with
// mode 0700 when it does not exist; files are written with mode 0600.
func NewFile(dir string) (*FileChannel, error) {
	if dir == "" {
		return nil, errors.New("file: directory not set")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("file: create directory: %w", err)
	}
	return &FileChannel{dir: dir}, nil
}

// Kind returns File.
func (c *FileChannel) Kind() Kind { return File }

// Send writes n as one Markdown file. The name holds the UTC time of the
// notification and the start of the hash of its Key, so sending the same
// notification again overwrites the same file with the same content.
func (c *FileChannel) Send(ctx context.Context, n Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.TenantID == "" || n.WatchID == "" {
		return errors.New("file: notification needs a tenant and a watch")
	}
	sum := sha256.Sum256([]byte(n.Key()))
	name := fmt.Sprintf("%s-%s-%s-%s.md",
		n.At.UTC().Format("20060102-150405"),
		safeName(n.TenantID), safeName(n.WatchID),
		hex.EncodeToString(sum[:])[:8])
	if err := writeAtomic(filepath.Join(c.dir, name), renderMarkdown(n)); err != nil {
		return fmt.Errorf("file: %w", err)
	}
	return nil
}

// safeName keeps lower-case letters, digits and hyphens and replaces every
// other character with a hyphen, so the value can sit in a file name.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, s)
}

var (
	mdLabel = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`)
	mdURL   = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E")
)

// renderMarkdown lays a notification out as Markdown: a heading, a list of
// its details, the body as given and a list of the articles with their links.
func renderMarkdown(n Notification) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s\n\n", oneLine(n.Title))
	fmt.Fprintf(&b, "- Tenant: %s\n", oneLine(n.TenantID))
	fmt.Fprintf(&b, "- Watch: %s\n", oneLine(n.WatchID))
	fmt.Fprintf(&b, "- Alert: %s\n", oneLine(n.Alert))
	fmt.Fprintf(&b, "- Time: %s\n\n", n.At.UTC().Format(time.RFC3339))
	if n.Body != "" {
		b.WriteString(strings.TrimRight(n.Body, "\n"))
		b.WriteString("\n\n")
	}
	for _, a := range n.Articles {
		fmt.Fprintf(&b, "- [%s](%s) — %s\n", mdLabel.Replace(oneLine(a.Title)), mdURL.Replace(a.URL), oneLine(a.Domain))
	}
	return b.Bytes()
}

// oneLine collapses all white space, line breaks included, so a value from
// an article cannot start a new line in the document.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// writeAtomic writes data to a temporary file in the same folder as path and
// renames it over path, so a reader sees either the old file or the new one.
// os.CreateTemp creates the file with mode 0600.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
