// Command stoxicity-tui is the full-screen interface. It is a separate program
// so plain stoxicity commands do not pay for terminal setup. The interface
// itself is not written yet: tui.Run returns an error.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/tenant"
	"github.com/b070nd/stoxicity/app/src/tui"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	root := flag.String("root", ".", "configuration folder")
	tenantFlag := flag.String("tenant", "", "tenant to open (default: $STOXICITY_TENANT, then \"default\")")
	watchFlag := flag.String("watch", "", "watch to open (default: the tenant's default_watch)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("stoxicity-tui", version)
		return
	}
	tenantID := tenant.Resolve(*tenantFlag, os.Getenv("STOXICITY_TENANT"), tenant.Default)
	if !model.ValidID(tenantID) || (*watchFlag != "" && !model.ValidID(*watchFlag)) {
		fmt.Fprintln(os.Stderr, "stoxicity-tui: bad tenant or watch ID")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := tui.Run(ctx, tui.Options{Root: *root, Tenant: tenantID, Watch: *watchFlag}); err != nil {
		fmt.Fprintln(os.Stderr, "stoxicity-tui:", err)
		os.Exit(1)
	}
}
