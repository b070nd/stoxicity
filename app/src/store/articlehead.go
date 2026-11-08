package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// ArticleHeads returns, for each article, a short fingerprint of what every
// source currently says about it: the newest observation of each source. A
// correction at the source gives the article a new head; an article that was
// only seen again gives the same one. Articles that are not stored are left out.
func (db *DB) ArticleHeads(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	h, err := db.baseHandle()
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		chunk := ids[start:end]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := h.r.QueryContext(ctx, `SELECT o.article_id, o.source_id, o.content_hash, o.title, o.summary FROM observation o
			WHERE o.article_id IN (`+marks+`)
			  AND o.seq = (SELECT MAX(seq) FROM observation WHERE article_id = o.article_id AND source_id = o.source_id)
			ORDER BY o.article_id, o.source_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("store: article heads: %w", err)
		}
		parts := map[string][]string{}
		for rows.Next() {
			var id, src, hash, title, summary string
			if err := rows.Scan(&id, &src, &hash, &title, &summary); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: article heads: %w", err)
			}
			if hash == "" { // rows made before content hashes existed
				sum := sha256.Sum256([]byte(title + "\x00" + summary))
				hash = hex.EncodeToString(sum[:8])
			}
			parts[id] = append(parts[id], src+"="+hash)
		}
		rows.Close()
		for id, p := range parts {
			sort.Strings(p)
			sum := sha256.Sum256([]byte(strings.Join(p, ";")))
			out[id] = hex.EncodeToString(sum[:8])
		}
	}
	return out, nil
}
