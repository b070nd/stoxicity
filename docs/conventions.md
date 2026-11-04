# Conventions for building the core

Several packages are written at the same time by different people or agents.
These rules keep them from colliding. Read this first, then your scope.

## Project

stoxicity is a stock-news aggregator for a long-term investor, written in Go.
The module `github.com/b070nd/stoxicity/app` is in `app/`; packages are in
`app/src/<pkg>`. Run Go commands from `app/`. Background reading: `README.md`,
`docs/fetchers.md`, `docs/configuration.md`, and the doc comments of the
packages you own or import. `docs/research/bleeding-edge-design.md` is
research; read only what you need. It has known errors: it named a SQLite
version that does not exist.

## Rules

1. **Stay in your scope.** Touch only the paths named in your scope. Other
   packages are being written in parallel.
2. **Dependencies are fixed.** Do not edit `go.mod` or `go.sum` and do not run
   `go mod tidy`. Available: the standard library, `go.yaml.in/yaml/v3`
   v3.0.6, `modernc.org/sqlite` v1.61.1, `golang.org/x/time` v0.16.0 and
   `golang.org/x/text` v0.43.0. If you need another module, use the standard
   library and say so in your report.
3. **Contracts.** Do not change an exported signature in a package you do not
   own. If a contract elsewhere is wrong or missing, work around it inside your
   scope and say so. In your own packages you may refine stubs, but keep the
   names and meaning of exported identifiers other packages already use,
   unless your scope says otherwise.
4. **Verify narrowly.** From `app/`: `gofmt -l src` (nothing of yours may be
   listed), `go vet ./src/<pkg>/...`, `go test -race -count=1 ./src/<pkg>/...`.
   Do not run `go build ./...` or `go test ./...`: other packages are
   mid-edit. If a failure is in a package outside your scope, wait 30 to 60
   seconds and retry; never edit it.
5. **No live network.** Never contact sec.gov or any real site. Tests use
   `httptest` or fakes. Fixtures you write are synthetic, from the documented
   format as you know it. Put "synthetic fixture, not verified against live
   data" in a comment at the top of each, and list them in your report.
6. **Style.** Match the surrounding code: plain-English doc comments on every
   exported identifier, errors lower-case and prefixed with the package name
   (`httpx: ...`), table-driven tests, no dead code, no TODO comments, no
   commented-out code, no frameworks. macOS and Linux only.
7. **Secrets.** Never read `.env.local`. Never print, log or put in an error
   message the value of a credential; names only.
8. **Report.** When done, reply with at most 250 words: files created or
   changed; what works; anything you did differently from the scope, and why;
   assumptions you could not verify; the final `go test` line. No code.
