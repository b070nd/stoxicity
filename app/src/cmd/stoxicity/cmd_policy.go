package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/policy"
)

// cmdPolicy is the investment policy: the example to start from, a check of a
// file, a preview against the policy in force, and the activation that makes a
// file the policy. It never places an order and never reads a broker.
func cmdPolicy(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: example | show | check | preview | activate | history")
	}
	switch args[0] {
	case "example":
		return policyExample(a, args[1:])
	case "show":
		return policyShow(ctx, a, args[1:])
	case "check":
		return policyCheck(a, args[1:])
	case "preview":
		return policyPreview(ctx, a, args[1:])
	case "activate":
		return policyActivate(ctx, a, args[1:])
	case "history":
		return policyHistory(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (example, show, check, preview, activate, history)", args[0])
}

func readPolicyFile(path string) (policy.Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return policy.Policy{}, err
	}
	defer f.Close()
	return policy.Parse(f)
}

func policyExample(a *app, args []string) error {
	fs := newFlags("policy example")
	profile := fs.String("profile", "conservative-eur-1000", "the example profile: "+strings.Join(policy.Profiles(), ", "))
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	p, err := policy.Example(*profile)
	if err != nil {
		return usageErrorf("%v", err)
	}
	b, err := policy.Marshal(p)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "# %s: an example, not advice. Read every value, replace what is wrong, give each\n"+
		"# fact you know a source (user, imported or estimate), leave the rest unknown, and set\n"+
		"# example to false. Then: stoxicity policy check -file FILE, preview, activate.\n", p.Name)
	_, err = a.stdout.Write(b)
	return err
}

func policyShow(ctx context.Context, a *app, args []string) error {
	fs := newFlags("policy show")
	at := fs.String("at", "", "the policy in force at this time (RFC 3339); default now")
	asJSON := fs.Bool("json", false, "print the record as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	var when time.Time
	if *at != "" {
		if when, err = time.Parse(time.RFC3339, *at); err != nil {
			return usageErrorf("-at: %v", err)
		}
	}
	st, err := a.policyStore()
	if err != nil {
		return err
	}
	rec, ok, err := st.AsOf(ctx, when)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(a.stdout, "no policy was in force for tenant %s %s\nstart with: stoxicity policy example\n", a.tenantID, map[bool]string{true: "at that time", false: "yet"}[*at != ""])
		return nil
	}
	if *asJSON {
		return writeJSON(a.stdout, rec)
	}
	fmt.Fprintf(a.stdout, "tenant %s, policy %q, epoch %d, hash %s\nin force since %s\n\n", a.tenantID, rec.Policy.Name, rec.Epoch, rec.Hash, rec.At.In(a.location()).Format(time.RFC3339))
	b, err := policy.Marshal(rec.Policy)
	if err != nil {
		return err
	}
	_, err = a.stdout.Write(b)
	writeUnknowns(a.stdout, policy.Unknowns(rec.Policy))
	return err
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeProblems(w io.Writer, ps []policy.Problem) {
	for _, p := range ps {
		fmt.Fprintf(w, "  %-7s %s: %s\n", p.Severity, p.Path, p.Message)
	}
}

func writeUnknowns(w io.Writer, us []policy.Unknown) {
	if len(us) == 0 {
		fmt.Fprintln(w, "\nno unknown facts")
		return
	}
	fmt.Fprintf(w, "\nunknown facts and what waits for them (%s):\n", policy.ResearchUnaffected)
	for _, u := range us {
		fmt.Fprintf(w, "  %s: %s\n", u.Path, strings.Join(u.Affects, "; "))
	}
}

func policyCheck(a *app, args []string) error {
	fs := newFlags("policy check")
	file := fs.String("file", "", "the policy file")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("want -file FILE")
	}
	p, err := readPolicyFile(*file)
	if err != nil {
		return err
	}
	pv, err := policy.MakePreview(p, nil, a.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "policy %q", p.Name)
	if pv.Hash != "" {
		fmt.Fprintf(a.stdout, ", hash %s", pv.Hash)
	}
	fmt.Fprintln(a.stdout)
	if len(pv.Problems) == 0 {
		fmt.Fprintln(a.stdout, "no findings")
	} else {
		fmt.Fprintln(a.stdout, "findings:")
		writeProblems(a.stdout, pv.Problems)
	}
	if pv.Hash != "" {
		writeUnknowns(a.stdout, pv.Unknowns)
	}
	if len(pv.Blocked()) > 0 {
		return errReported
	}
	return nil
}

func policyPreview(ctx context.Context, a *app, args []string) error {
	fs := newFlags("policy preview")
	file := fs.String("file", "", "the policy file")
	asJSON := fs.Bool("json", false, "print the preview as JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" {
		return usageErrorf("want -file FILE")
	}
	p, err := readPolicyFile(*file)
	if err != nil {
		return err
	}
	st, err := a.policyStore()
	if err != nil {
		return err
	}
	cur, ok, err := st.AsOf(ctx, time.Time{})
	if err != nil {
		return err
	}
	var active *policy.Record
	if ok {
		active = &cur
	}
	pv, err := policy.MakePreview(p, active, a.now())
	if err != nil {
		return err
	}
	if *asJSON {
		if err := writeJSON(a.stdout, pv); err != nil {
			return err
		}
	} else {
		writePreview(a, p, pv)
	}
	if len(pv.Blocked()) > 0 {
		return errReported
	}
	return nil
}

func writePreview(a *app, p policy.Policy, pv policy.Preview) {
	w := a.stdout
	fmt.Fprintf(w, "candidate %q\n", p.Name)
	if len(pv.Problems) > 0 {
		fmt.Fprintln(w, "findings:")
		writeProblems(w, pv.Problems)
	}
	if blocked := pv.Blocked(); len(blocked) > 0 {
		fmt.Fprintln(w, "\ncannot be activated until the errors are fixed")
		return
	}
	if pv.Active != nil {
		fmt.Fprintf(w, "\nin force: epoch %d, hash %s, since %s\n", pv.Active.Epoch, pv.Active.Hash, pv.Active.At.In(a.location()).Format(time.RFC3339))
	} else {
		fmt.Fprintln(w, "\nno policy is in force yet")
	}
	if pv.Unchanged {
		fmt.Fprintln(w, "the candidate is identical to the policy in force; activating it records nothing")
		return
	}
	if len(pv.Changes) > 0 {
		fmt.Fprintf(w, "\n%d changes:\n", len(pv.Changes))
		for _, c := range pv.Changes {
			fmt.Fprintf(w, "  %s: %s -> %s\n", c.Path, c.Old, c.New)
		}
	}
	if len(pv.RiskIncreases) > 0 {
		fmt.Fprintln(w, "\nthese changes let the portfolio take more risk or know less before it acts, and need -accept-risk:")
		for _, r := range pv.RiskIncreases {
			fmt.Fprintf(w, "  %s\n", r)
		}
	}
	if len(pv.Effects) > 0 {
		fmt.Fprintln(w, "\nactivating it:")
		for _, e := range pv.Effects {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	writeUnknowns(w, pv.Unknowns)
	fmt.Fprintf(w, "\nto activate exactly this: stoxicity policy activate -file FILE -confirm %s", pv.Hash)
	if len(pv.RiskIncreases) > 0 {
		fmt.Fprint(w, " -accept-risk")
	}
	fmt.Fprintln(w)
}

func policyActivate(ctx context.Context, a *app, args []string) error {
	fs := newFlags("policy activate")
	file := fs.String("file", "", "the policy file")
	confirm := fs.String("confirm", "", "the hash the preview printed")
	risk := fs.Bool("accept-risk", false, "accept the changes that raise risk")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *file == "" || *confirm == "" {
		return usageErrorf("want -file FILE -confirm HASH (preview prints the hash)")
	}
	p, err := readPolicyFile(*file)
	if err != nil {
		return err
	}
	st, err := a.policyStore()
	if err != nil {
		return err
	}
	prevTier, _, err := policyTier(ctx, a)
	if err != nil {
		return err
	}
	if err := requireTierDecision(ctx, a, prevTier, p.Tier); err != nil {
		return err
	}
	rec, err := policy.Activate(ctx, st, p, *confirm, *risk, a.now())
	if err != nil {
		return err
	}
	if p.Tier < prevTier {
		n, err := revokeNewRisk(ctx, a, fmt.Sprintf("the tier was lowered from %d to %d", prevTier, p.Tier))
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "tier lowered from %d to %d: %d unsent confirmation(s) revoked; holdings and open orders are untouched.\n", prevTier, p.Tier, n)
	}
	fmt.Fprintf(a.stdout, "policy %q is in force for tenant %s: epoch %d, hash %s\n", rec.Policy.Name, a.tenantID, rec.Epoch, rec.Hash)
	for _, e := range rec.Effects {
		fmt.Fprintf(a.stdout, "  %s\n", e)
	}
	return nil
}

func policyHistory(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 {
		return usageErrorf("unexpected argument %q", args[0])
	}
	st, err := a.policyStore()
	if err != nil {
		return err
	}
	hist, err := st.History(ctx)
	if err != nil {
		return err
	}
	if len(hist) == 0 {
		fmt.Fprintf(a.stdout, "no policy was ever activated for tenant %s\n", a.tenantID)
		return nil
	}
	t := cliTable{Header: []string{"epoch", "since", "name", "hash", "changes", "risk accepted"}, Right: []bool{true}}
	for _, r := range hist {
		acc := "no"
		if r.RiskAccepted {
			acc = "yes"
		}
		t.Rows = append(t.Rows, []cliCell{plain(fmt.Sprint(r.Epoch)), plain(r.At.In(a.location()).Format(time.RFC3339)),
			plain(r.Policy.Name), plain(r.Hash), plain(fmt.Sprint(len(r.Changes))), plain(acc)})
	}
	t.write(a.stdout, useColor(a.stdout))
	return nil
}
