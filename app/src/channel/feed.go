package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	feedID         = "urn:stoxicity:feed"
	defaultFeedMax = 100
)

// FeedChannel keeps the newest notifications in an Atom 1.0 file that a feed
// reader can subscribe to.
type FeedChannel struct {
	mu    sync.Mutex
	path  string
	title string
	self  string
	max   int
}

var _ Channel = (*FeedChannel)(nil)

// NewFeed returns a channel that maintains the Atom feed at path. title names
// the feed; selfURL is the address readers subscribe to and may be empty. The
// feed keeps at most max entries, newest first; zero or less means 100.
func NewFeed(path, title, selfURL string, max int) (*FeedChannel, error) {
	if path == "" {
		return nil, errors.New("feed: path not set")
	}
	if title == "" {
		return nil, errors.New("feed: title not set")
	}
	if max <= 0 {
		max = defaultFeedMax
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("feed: create directory: %w", err)
	}
	return &FeedChannel{path: path, title: title, self: selfURL, max: max}, nil
}

// Kind returns Feed.
func (c *FeedChannel) Kind() Kind { return Feed }

// Send makes n the newest entry of the feed. An entry with the same id as an
// existing one replaces it rather than repeating it. If the existing file is
// not a valid Atom feed, Send returns an error and leaves the file alone.
func (c *FeedChannel) Send(ctx context.Context, n Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, err := c.load()
	if err != nil {
		return err
	}
	e := newEntry(n)
	kept := make([]atomEntry, 0, len(existing)+1)
	kept = append(kept, e)
	for _, old := range existing {
		if old.ID != e.ID {
			kept = append(kept, old)
		}
	}
	if len(kept) > c.max {
		kept = kept[:c.max]
	}
	data, err := c.encode(kept)
	if err != nil {
		return err
	}
	if err := writeAtomic(c.path, data); err != nil {
		return fmt.Errorf("feed: %w", err)
	}
	return nil
}

// load reads the entries of the current feed file. A missing file is an empty
// feed; a file that does not parse as Atom is an error.
func (c *FeedChannel) load() ([]atomEntry, error) {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("feed: read: %w", err)
	}
	var doc atomFeed
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("feed: %s is not a valid Atom feed, not overwritten: %w", c.path, err)
	}
	return doc.Entries, nil
}

// encode renders the whole feed. The feed's own updated time is the newest
// entry's.
func (c *FeedChannel) encode(entries []atomEntry) ([]byte, error) {
	doc := atomFeed{
		ID:      feedID,
		Title:   c.title,
		Updated: latest(entries),
		Author:  atomAuthor{Name: "stoxicity"},
		Entries: entries,
	}
	if c.self != "" {
		doc.Links = []atomLink{{Rel: "self", Href: c.self}}
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("feed: encode: %w", err)
	}
	return append([]byte(xml.Header), out...), nil
}

// newEntry builds the Atom entry for n. Its id is a tag URI made of the date
// and the hash of the notification's Key, so the same event keeps its id.
func newEntry(n Notification) atomEntry {
	sum := sha256.Sum256([]byte(n.Key()))
	at := n.At.UTC().Truncate(time.Second)
	e := atomEntry{
		ID:      "tag:stoxicity," + at.Format("2006-01-02") + ":" + hex.EncodeToString(sum[:]),
		Title:   n.Title,
		Updated: at,
		Summary: atomText{Type: "text", Text: n.Body},
	}
	for _, a := range n.Articles {
		e.Links = append(e.Links, atomLink{Rel: "related", Href: a.URL, Title: a.Title})
	}
	return e
}

// latest returns the newest Updated time among entries, or the zero time.
func latest(entries []atomEntry) time.Time {
	var t time.Time
	for _, e := range entries {
		if e.Updated.After(t) {
			t = e.Updated
		}
	}
	return t
}

// atomFeed is the part of an Atom 1.0 feed this package reads and writes.
type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated time.Time   `xml:"updated"`
	Author  atomAuthor  `xml:"author"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomEntry struct {
	ID      string     `xml:"id"`
	Title   string     `xml:"title"`
	Updated time.Time  `xml:"updated"`
	Links   []atomLink `xml:"link"`
	Summary atomText   `xml:"summary"`
}

type atomLink struct {
	Rel   string `xml:"rel,attr,omitempty"`
	Href  string `xml:"href,attr"`
	Title string `xml:"title,attr,omitempty"`
}

type atomText struct {
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}
