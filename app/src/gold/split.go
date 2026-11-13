package gold

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Side is the part an item belongs to under a split with the seed and the
// development fraction: development when a number between 0 and 1 made from the
// seed and the ID is below the fraction, held out otherwise. It depends on
// nothing else, so an item keeps its part whatever else is in the file.
func Side(seed int64, devFraction float64, id string) string {
	if float64(rank("split", seed, id))/math.Exp2(64) < devFraction {
		return RoleDev
	}
	return RoleTest
}

// Split divides a labelled file into a development part, on which rules may be
// tuned, and a held-out test part, by Side. Both files record the rule, the seed
// and a digest of the IDs that were split. It fails when the fraction is not
// strictly between 0 and 1, when an item is not labelled or the file does not
// pass Check, when the file is itself a part of an earlier split, or when either
// part would be empty.
func Split(f File, seed int64, devFraction float64) (dev, test File, err error) {
	switch {
	case !(devFraction > 0 && devFraction < 1):
		return File{}, File{}, errors.New("gold: the development fraction must be above 0 and below 1")
	case f.Header.Role != "":
		return File{}, File{}, fmt.Errorf("gold: the file is the %s part of an earlier split and cannot be split again", f.Header.Role)
	case len(f.Items) == 0:
		return File{}, File{}, errors.New("gold: the file has no items")
	}
	if c := Count(f); c.Labelled < c.Items {
		return File{}, File{}, fmt.Errorf("gold: %d of %d items are not labelled; label them all before the split", c.Items-c.Labelled, c.Items)
	}
	if ps := Check(f); len(ps) > 0 {
		return File{}, File{}, fmt.Errorf("gold: the labels have %d problems (gold check lists them), first: %s", len(ps), ps[0])
	}
	ids := make([]string, 0, len(f.Items))
	for _, it := range f.Items {
		ids = append(ids, it.ID)
	}
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	rule := SplitRule{
		Seed: seed, DevFraction: devFraction, Population: hex.EncodeToString(sum[:]), Items: len(ids),
		Rule: "an item is development when the first eight bytes of the SHA-256 of purpose \"split\", seed and item ID, " +
			"read as a number from 0 to 1, are below the development fraction; held out otherwise",
	}
	dev.Header = Header{Format: Version, Role: RoleDev, Drawn: f.Header.Drawn, Split: &rule}
	test.Header = Header{Format: Version, Role: RoleTest, Drawn: f.Header.Drawn, Split: &rule}
	for _, it := range f.Items {
		if Side(seed, devFraction, it.ID) == RoleDev {
			dev.Items = append(dev.Items, it)
		} else {
			test.Items = append(test.Items, it)
		}
	}
	if len(dev.Items) == 0 || len(test.Items) == 0 {
		return File{}, File{}, fmt.Errorf("gold: this split leaves a part empty (%d development, %d test items); label more items or change the fraction", len(dev.Items), len(test.Items))
	}
	return dev, test, nil
}
