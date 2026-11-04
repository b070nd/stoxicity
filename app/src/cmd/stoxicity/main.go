// Command stoxicity aggregates stock-market news for a long-term investor.
//
// Fetching works on the shared information base and takes no tenant. Everything
// else runs for a tenant (-tenant or STOXICITY_TENANT, default "default").
//
//	stoxicity fetch [source-id...]     poll sources once and store what they have
//	stoxicity run                      poll the enabled sources until interrupted
//	stoxicity digest [-day DAY]        the day's relevant news, domains and trends
//	stoxicity search QUERY             filter stored news with a Google-style query
//	stoxicity watch list|check|test    list, validate or try the tenant's watches
//	stoxicity tenant list              list tenants
//	stoxicity sources list             list sources with their last health
//	stoxicity doctor                   check the installation
//	stoxicity version                  print the version
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, runOptions{})
	stop()
	os.Exit(code)
}
