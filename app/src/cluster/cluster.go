// Package cluster groups articles about the same event, so a story syndicated
// across many domains counts once. Without it the per-domain counts reward
// copying. Matching goes from the canonical URL to title shingles.
package cluster

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"math/bits"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/model"
)

const (
	minTitleWords = 5              // titles with fewer normalised words skip the title match
	shingleSize   = 3              // words per title shingle
	maxHamming    = 3              // largest SimHash Hamming distance that counts as a near-duplicate
	publishWindow = 48 * time.Hour // largest gap between publication times of near-duplicates
)

// stopwords are dropped from titles before they are compared.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "and": true, "or": true,
	"to": true, "in": true, "on": true, "for": true, "with": true, "at": true,
	"by": true, "from": true, "as": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "its": true, "it": true, "this": true,
	"that": true,
}

// trackingNames are tracking query parameters removed by exact name, in lower case.
// Parameters whose name starts with "utm_" are removed as well.
var trackingNames = map[string]bool{
	"fbclid": true, "gclid": true, "mc_cid": true, "mc_eid": true, "ref": true,
	"ref_src": true, "cmpid": true, "cid": true, "igshid": true, "yclid": true,
	"_hsenc": true, "_hsmi": true,
}

// Group is one event and the articles that report it.
type Group struct {
	ID       string
	Original model.Article   // earliest publisher; it gets the credit
	Members  []model.Article // everything else, including Original
}

// Build groups articles that report the same event and sets ClusterID on every
// member. The result does not depend on the order of the input. Each group's
// Original is its earliest publication, and the groups are sorted by Original.
func Build(articles []model.Article) []Group {
	groups, _ := group(articles)
	return groups
}

// Assign returns copies of articles, in input order, with ClusterID set from
// Build. Every article gets a cluster, singletons included. The inputs are not
// modified.
func Assign(articles []model.Article) []model.Article {
	_, ids := group(articles)
	out := slices.Clone(articles)
	for i := range out {
		out[i].ClusterID = ids[i]
	}
	return out
}

// Canonical returns rawURL in a form where copies of the same story compare
// equal. It lower-cases the scheme and host, drops a leading "www." and the
// default port, drops the fragment, removes tracking parameters, sorts the
// remaining parameters by name and strips one trailing slash from a path longer
// than "/". If rawURL does not parse, it is returned unchanged.
func Canonical(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // an IPv6 literal keeps its brackets
	}
	if port != "" {
		host += ":" + port
	}

	query := u.Query()
	for name := range query {
		if isTracking(name) {
			delete(query, name)
		}
	}

	path := u.Path
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}

	u.Scheme = scheme
	u.Host = host
	u.Path = path
	u.RawPath = ""
	u.RawQuery = query.Encode() // Encode sorts the parameters by name
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// isTracking reports whether a query parameter only records where a click came from.
func isTracking(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "utm_") || trackingNames[name]
}

// forest is a union-find structure over article positions. The root of each set
// is its lowest position, so the sets do not depend on the order of the unions.
type forest []int

// find returns the root of the set that holds i, halving paths as it goes.
func (f forest) find(i int) int {
	for f[i] != i {
		f[i] = f[f[i]]
		i = f[i]
	}
	return i
}

// union joins the sets that hold a and b.
func (f forest) union(a, b int) {
	ra, rb := f.find(a), f.find(b)
	if ra != rb {
		f[max(ra, rb)] = min(ra, rb)
	}
}

// group does the work of Build and Assign. It returns the groups, and for each
// input index the ID of the group that article belongs to.
func group(articles []model.Article) ([]Group, []string) {
	// Sorting by ID first gives the union-find a fixed starting order. order[p]
	// is the input index of the article at sorted position p.
	order := make([]int, len(articles))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(articles[a].ID, articles[b].ID)
	})
	arts := make([]model.Article, len(order))
	for p, i := range order {
		arts[p] = articles[i]
	}

	f := forest(make([]int, len(arts)))
	for p := range f {
		f[p] = p
	}

	// Stage 1: the same ID, or the same canonical URL, is the same story.
	byID := make(map[string]int)
	byURL := make(map[string]int)
	for p, a := range arts {
		if a.ID != "" {
			if q, ok := byID[a.ID]; ok {
				f.union(q, p)
			} else {
				byID[a.ID] = p
			}
		}
		if a.URL != "" {
			key := Canonical(a.URL)
			if q, ok := byURL[key]; ok {
				f.union(q, p)
			} else {
				byURL[key] = p
			}
		}
	}

	matchTitles(arts, f)

	// Collect each set's members, in the order their roots first appear.
	var roots []int
	members := make(map[int][]int)
	for p := range arts {
		r := f.find(p)
		if _, ok := members[r]; !ok {
			roots = append(roots, r)
		}
		members[r] = append(members[r], p)
	}

	groups := make([]Group, 0, len(roots))
	ids := make([]string, len(articles))
	for _, r := range roots {
		list := make([]model.Article, 0, len(members[r]))
		for _, p := range members[r] {
			list = append(list, arts[p])
		}
		slices.SortStableFunc(list, compareArticles)
		id := groupID(list[0].ID)
		for k := range list {
			list[k].ClusterID = id
		}
		for _, p := range members[r] {
			ids[order[p]] = id
		}
		groups = append(groups, Group{ID: id, Original: list[0], Members: list})
	}
	slices.SortStableFunc(groups, func(a, b Group) int {
		return compareArticles(a.Original, b.Original)
	})
	return groups, ids
}

// fingerprint holds what stage 2 needs to compare two titles.
type fingerprint struct {
	pos       int // position in the sorted article slice
	hash      uint64
	words     map[string]struct{} // distinct normalised words
	published time.Time
}

// matchTitles is stage 2. It unites near-duplicate titles in f.
//
// Comparing every pair is quadratic, so articles are bucketed by the four 16-bit
// bands of their SimHash. Two SimHashes within Hamming distance 3 differ in at
// most 3 of the 4 bands, so they agree on at least one band and share a bucket.
// No near-duplicate is missed, and only articles that share a band are compared.
func matchTitles(arts []model.Article, f forest) {
	var bands [4]map[uint16][]int
	for b := range bands {
		bands[b] = make(map[uint16][]int)
	}
	var fps []fingerprint
	for p, a := range arts {
		if a.Published.IsZero() {
			continue // a zero time never matches on title
		}
		ws := titleWords(a.Title)
		if len(ws) < minTitleWords {
			continue
		}
		set := make(map[string]struct{}, len(ws))
		for _, w := range ws {
			set[w] = struct{}{}
		}
		fp := fingerprint{pos: p, hash: simhash(ws), words: set, published: a.Published}
		for b := range bands {
			key := uint16(fp.hash >> (16 * b))
			bands[b][key] = append(bands[b][key], len(fps))
		}
		fps = append(fps, fp)
	}

	for _, bucket := range bands {
		for _, members := range bucket {
			for i := range members {
				for j := i + 1; j < len(members); j++ {
					x, y := &fps[members[i]], &fps[members[j]]
					if f.find(x.pos) != f.find(y.pos) && near(x, y) {
						f.union(x.pos, y.pos)
					}
				}
			}
		}
	}
}

// near reports whether two titles are near-duplicates: SimHashes within
// maxHamming, word sets with a Jaccard similarity of at least 3/4, and
// publication times no more than publishWindow apart.
func near(a, b *fingerprint) bool {
	lo, hi := a.published, b.published
	if lo.After(hi) {
		lo, hi = hi, lo
	}
	if hi.Sub(lo) > publishWindow {
		return false
	}
	if bits.OnesCount64(a.hash^b.hash) > maxHamming {
		return false
	}
	return jaccardAtLeastThreeQuarters(a.words, b.words)
}

// jaccardAtLeastThreeQuarters reports whether |a∩b| / |a∪b| is at least 3/4.
// It compares integers so the threshold is exact.
func jaccardAtLeastThreeQuarters(a, b map[string]struct{}) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	shared := 0
	for w := range a {
		if _, ok := b[w]; ok {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	return 4*shared >= 3*union
}

// titleWords returns the normalised words of a title: lower-case, every run of
// letters and digits as one word, English stopwords removed.
func titleWords(title string) []string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, strings.ToLower(title))
	var ws []string
	for _, w := range strings.Fields(clean) {
		if !stopwords[w] {
			ws = append(ws, w)
		}
	}
	return ws
}

// simhash returns a 64-bit SimHash of the distinct word shingles in ws. Each
// shingle is hashed with FNV-1a 64 and votes with weight 1 on every bit. A bit
// is set when more shingles vote for 1 than for 0, so a tie leaves it unset.
func simhash(ws []string) uint64 {
	var votes [64]int
	seen := make(map[string]bool)
	for i := 0; i+shingleSize <= len(ws); i++ {
		s := strings.Join(ws[i:i+shingleSize], " ")
		if seen[s] {
			continue
		}
		seen[s] = true
		h := fnv.New64a()
		h.Write([]byte(s)) // hash.Hash never returns an error
		sum := h.Sum64()
		for b := range votes {
			if (sum>>b)&1 == 1 {
				votes[b]++
			} else {
				votes[b]--
			}
		}
	}
	var out uint64
	for b, v := range votes {
		if v > 0 {
			out |= 1 << b
		}
	}
	return out
}

// compareArticles orders articles by publication time, with a zero time last.
// Ties fall back to ID and then the other fields, so the order is total.
func compareArticles(a, b model.Article) int {
	if az, bz := a.Published.IsZero(), b.Published.IsZero(); az != bz {
		if az {
			return 1
		}
		return -1
	}
	return cmp.Or(
		a.Published.Compare(b.Published),
		cmp.Compare(a.ID, b.ID),
		cmp.Compare(a.URL, b.URL),
		cmp.Compare(a.Title, b.Title),
		cmp.Compare(a.SourceID, b.SourceID),
		cmp.Compare(a.Domain, b.Domain),
	)
}

// groupID is "c-" followed by the first 12 hex characters of the SHA-256 of the
// original article's ID.
func groupID(originalID string) string {
	sum := sha256.Sum256([]byte(originalID))
	return "c-" + hex.EncodeToString(sum[:])[:12]
}
