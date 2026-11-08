package plan

import "math/big"

func ratOf(s string) (*big.Rat, bool) {
	r, ok := new(big.Rat).SetString(s)
	return r, ok
}
