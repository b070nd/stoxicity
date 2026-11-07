package main

import (
	"encoding/json"

	"github.com/b070nd/stoxicity/app/src/assertion/challenge"
)

// cmdEvalClaims runs the claim reader on the embedded challenge corpus and prints
// each stage on its own, the abstentions and every error. It reads no database
// and makes no request.
func cmdEvalClaims(a *app, args []string) error {
	fs := newFlags("eval claims")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	corpus, err := challenge.Load()
	if err != nil {
		return err
	}
	rep := challenge.Run(corpus)
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	rep.Write(a.stdout)
	return nil
}
