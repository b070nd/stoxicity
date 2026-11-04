package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
)

// gmailTokenFile is where "gmail auth" keeps the refresh token, under the data
// folder (which git ignores). It is a credential: the folder is 0700 and the
// file 0600, and no command prints its content.
func (a *app) gmailTokenFile() string {
	return filepath.Join(a.dataDir(), "gmail", "refresh_token")
}

// gmailRefreshToken returns the refresh token: the variable
// STOXICITY_GMAIL_REFRESH_TOKEN if set, else the file; empty if neither.
func (a *app) gmailRefreshToken() string {
	if v := strings.TrimSpace(os.Getenv("STOXICITY_GMAIL_REFRESH_TOKEN")); v != "" {
		return v
	}
	raw, err := os.ReadFile(a.gmailTokenFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// gmailSettings are the values the email channel is built from.
func (a *app) gmailSettings() channel.Settings {
	return channel.Settings{
		GmailClientID:     os.Getenv("STOXICITY_GMAIL_CLIENT_ID"),
		GmailClientSecret: os.Getenv("STOXICITY_GMAIL_CLIENT_SECRET"),
		GmailRefreshToken: a.gmailRefreshToken(),
		MailFrom:          os.Getenv("STOXICITY_MAIL_FROM"),
		MailTo:            os.Getenv("STOXICITY_MAIL_TO"),
		GmailTokenURL:     a.opts.gmailTokenURL,
		GmailSendURL:      a.opts.gmailSendURL,
	}
}

// cmdGmail sets up and checks the email channel.
func cmdGmail(ctx context.Context, a *app, args []string) error {
	if len(args) != 1 {
		return usageErrorf("expected: status | auth | test")
	}
	switch args[0] {
	case "status":
		return a.gmailStatus()
	case "auth":
		return a.gmailAuth(ctx)
	case "test":
		return a.gmailTest(ctx)
	}
	return usageErrorf("unknown subcommand %q (expected status, auth or test)", args[0])
}

// gmailStatus says which values are set, by name, and never shows one.
func (a *app) gmailStatus() error {
	set := func(name string) string {
		if os.Getenv(name) != "" {
			return "set"
		}
		return "EMPTY"
	}
	fmt.Fprintf(a.stdout, "STOXICITY_GMAIL_CLIENT_ID      %s\n", set("STOXICITY_GMAIL_CLIENT_ID"))
	fmt.Fprintf(a.stdout, "STOXICITY_GMAIL_CLIENT_SECRET  %s (optional for a desktop client)\n", set("STOXICITY_GMAIL_CLIENT_SECRET"))
	fmt.Fprintf(a.stdout, "STOXICITY_MAIL_FROM            %s\n", set("STOXICITY_MAIL_FROM"))
	fmt.Fprintf(a.stdout, "STOXICITY_MAIL_TO              %s\n", set("STOXICITY_MAIL_TO"))
	switch {
	case os.Getenv("STOXICITY_GMAIL_REFRESH_TOKEN") != "":
		fmt.Fprintln(a.stdout, "authorisation                  from STOXICITY_GMAIL_REFRESH_TOKEN")
	case a.gmailRefreshToken() != "":
		fmt.Fprintf(a.stdout, "authorisation                  stored in %s\n", a.gmailTokenFile())
	default:
		fmt.Fprintln(a.stdout, `authorisation                  none yet: run "stoxicity gmail auth"`)
	}
	fmt.Fprintln(a.stdout, "\nA token made while the Google consent screen is in the Testing status stops working after 7 days; see docs/configuration.md.")
	return nil
}

// gmailAuth runs the one-time authorisation and stores the refresh token.
func (a *app) gmailAuth(ctx context.Context) error {
	open := a.opts.openURL
	if open == nil {
		open = openBrowser
	}
	token, err := channel.Authorize(ctx, channel.AuthConfig{
		ClientID:     os.Getenv("STOXICITY_GMAIL_CLIENT_ID"),
		ClientSecret: os.Getenv("STOXICITY_GMAIL_CLIENT_SECRET"),
		AuthURL:      a.opts.gmailAuthURL,
		TokenURL:     a.opts.gmailTokenURL,
		Show: func(u string) {
			fmt.Fprintln(a.stdout, "Open this address in a browser and approve \"Send email on your behalf\":")
			fmt.Fprintf(a.stdout, "\n%s\n\nWaiting for the browser (5 minutes)...\n", u)
			if err := open(u); err != nil {
				fmt.Fprintln(a.stdout, "(could not open a browser by itself; open the address above yourself)")
			}
		},
	})
	if err != nil {
		return err
	}
	path := a.gmailTokenFile()
	if err := writePrivate(path, []byte(token+"\n")); err != nil {
		return fmt.Errorf("store the authorisation: %w", err)
	}
	fmt.Fprintf(a.stdout, "Authorised. The refresh token is stored in %s (owner-only); it is not shown.\n", path)
	fmt.Fprintln(a.stdout, `Check with "stoxicity gmail test".`)
	return nil
}

// gmailTest sends one message to STOXICITY_MAIL_TO.
func (a *app) gmailTest(ctx context.Context) error {
	built, errs := channel.Build([]channel.Kind{channel.Email}, a.gmailSettings(), nil)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(a.stderr, "stoxicity: %v\n", e)
		}
		return errReported
	}
	n := channel.Notification{
		TenantID: a.tenantID, WatchID: "gmail-test", Alert: "test",
		Title: "stoxicity: test message",
		Body:  "If you read this, the email channel works.",
		At:    a.now(),
	}
	if err := built[channel.Email].Send(ctx, n); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "Sent a test message to STOXICITY_MAIL_TO.")
	return nil
}

// writePrivate writes data to path with mode 0600 in a folder of mode 0700,
// through a temporary file and a rename.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// openBrowser asks the system to open u. It is a convenience: the address is
// always printed too.
func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "linux":
		cmd = exec.Command("xdg-open", u)
	default:
		return fs.ErrInvalid
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}()
	return nil
}
