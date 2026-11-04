// Command stoxicity-tui is the full-screen interface. It is a separate program
// so plain stoxicity commands do not pay for terminal setup.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/tui"
	"github.com/b070nd/stoxicity/app/src/workspace"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	root := flag.String("root", "", "configuration folder (default: the closest one above the working directory)")
	tenantFlag := flag.String("tenant", "", "tenant to open (default: $STOXICITY_TENANT, then \"default\")")
	watchFlag := flag.String("watch", "", "watch to open (default: the tenant's default_watch)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("stoxicity-tui", version)
		return
	}
	if (*tenantFlag != "" && !model.ValidID(*tenantFlag)) || (*watchFlag != "" && !model.ValidID(*watchFlag)) {
		fmt.Fprintln(os.Stderr, "stoxicity-tui: bad tenant or watch ID")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *root, *tenantFlag, *watchFlag); err != nil {
		fmt.Fprintln(os.Stderr, "stoxicity-tui:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root, tenantID, watchID string) error {
	ws, err := workspace.Open(workspace.Options{Root: root, Tenant: tenantID, Watch: watchID})
	if err != nil {
		return err
	}
	defer ws.Close()
	return tui.Run(ctx, tui.Options{Backend: ws})
}
