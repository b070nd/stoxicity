package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	supervise "github.com/b070nd/stoxicity/app/src/sidecar"
)

// layaLog is the file the laya server writes to, in the data folder.
const (
	layaLog = "laya.log"
	layaPid = "laya.pid"
)

// startLaya makes sure the laya server is up for as long as the command runs
// and returns the function that ends it; call it when the command is done.
//
// Nothing happens when laya is switched off, when laya.manage is false, or when
// the address is not on this machine. A server that already answers is left as
// it is, and left running at the end. Otherwise the server is started on the
// loopback address with STOXICITY_LAYA_API_KEY as its key, and stopped by the
// returned function. A server that cannot be started is reported on stderr and
// the command goes on with the rules alone, as it does when the server is
// simply not there.
func (a *app) startLaya(ctx context.Context) (stop func()) {
	stop = func() {}
	l := a.cfg.Laya
	if !l.Enabled || !l.Manage || a.opts.enricher != nil {
		return stop
	}
	u, err := url.Parse(l.URL)
	if err != nil || !isLoopback(u.Hostname()) {
		fmt.Fprintln(a.stdout, "laya: its address is not on this machine; not starting it")
		return stop
	}
	scorer, err := a.layaScorer()
	if err != nil {
		return stop // enricher() says why
	}

	port := u.Port()
	if port == "" {
		port = "8000"
	}
	env := []string{
		"LAYA_HOST=" + u.Hostname(), "LAYA_PORT=" + port,
		"LAYA_PRELOAD=1",   // the checkpoints are loaded and named in /health before the first request
		"LAYA_AUTO_TASK=0", // no routing by guesswork between checkpoints
		"PYTHONHASHSEED=0", // set and dict order inside the server
		"TOKENIZERS_PARALLELISM=false",
	}
	if l.Device != "" {
		env = append(env, "LAYA_DEVICE="+l.Device)
	}
	if l.Threads > 0 {
		env = append(env, "LAYA_THREADS="+strconv.Itoa(l.Threads))
	}
	if l.Offline {
		env = append(env, "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1")
	}
	if l.Model != "" {
		env = append(env, "LAYA_MODELS="+l.Model, "LAYA_DEFAULT_MODEL="+l.Model)
	}
	if key := os.Getenv("STOXICITY_LAYA_API_KEY"); key != "" {
		env = append(env, "LAYA_API_KEY="+key)
	}
	logPath := filepath.Join(a.dataDir(), layaLog)
	spec := supervise.Spec{
		Name: "laya", Command: l.Command, Env: env, LogPath: logPath,
		PidFile:      filepath.Join(a.dataDir(), layaPid),
		Ready:        scorer.Ping,
		StartTimeout: a.cfg.LayaStartTimeout(),
	}

	var p *supervise.Process
	if scorer.Ping(ctx) == nil {
		// One that an earlier run left behind (it was killed before it could
		// stop the server) is ours to end; any other is not.
		if p = supervise.Adopt(spec); p == nil {
			fmt.Fprintln(a.stdout, "laya: already running; leaving it as it is")
			return stop
		}
		fmt.Fprintln(a.stdout, "laya: taking over the server an earlier run left behind")
	} else {
		fmt.Fprintf(a.stdout, "laya: starting %s (the first start loads the models; output in %s)\n", l.Command, a.relative(logPath))
		began := a.now()
		if p, err = supervise.Start(ctx, spec); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(a.stderr, "stoxicity: %v; scoring with the rules alone. Set laya.command or STOXICITY_LAYA_COMMAND, or laya.manage: false to start it yourself\n", err)
			}
			return stop
		}
		fmt.Fprintf(a.stdout, "laya: ready after %s\n", a.now().Sub(began).Round(100*time.Millisecond))
	}

	var stopping atomic.Bool
	go func() {
		<-p.Done()
		if !stopping.Load() {
			fmt.Fprintf(a.stderr, "stoxicity: laya stopped by itself; scoring with the rules alone from now on (see %s)\n", a.relative(logPath))
		}
	}()
	return func() {
		stopping.Store(true)
		p.Stop()
		fmt.Fprintln(a.stdout, "laya: stopped")
	}
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
