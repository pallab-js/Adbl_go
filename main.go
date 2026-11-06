package main

import (
	"context"
	"embed"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ubl/adbl/internal/blocklist"
	"github.com/ubl/adbl/internal/config"
	"github.com/ubl/adbl/internal/dnsengine"
	"github.com/ubl/adbl/internal/service"
	"github.com/ubl/adbl/internal/store"
	"github.com/ubl/adbl/internal/systemdns"
)

// Wails embeds the built frontend into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

const listenAddr = "127.0.0.1:5453"

func init() {
	// Typed events for the TS bindings (see docs/TRD.md §7).
	application.RegisterEvent[[]Query]("dns:batch")
	application.RegisterEvent[service.StatsTick]("stats:tick")
	application.RegisterEvent[[]blocklist.Status]("blocklist:status")
	application.RegisterEvent[service.DNSStatus]("dns:state")
	application.RegisterEvent[systemdns.Status]("system:state")
	application.RegisterEvent[service.Rules]("rules:changed")
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- Paths -----------------------------------------------------------------
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			log.Fatal(herr)
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	cfgPath := filepath.Join(cfgDir, "adbl", "config.json")
	dataPath := filepath.Join(dataDir, "adbl")

	// --- Core ------------------------------------------------------------------
	cfg, err := config.Open(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	db, err := store.Open(filepath.Join(dataPath, "adbl.db"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()

	blkStore := blocklist.NewStore()
	sys := systemdns.New(filepath.Join(dataPath, "state.json"))

	// Late-bound: services are built before the app exists.
	var app *application.App
	emit := func(name string, data any) {
		if app != nil {
			app.Event.Emit(name, data)
		}
	}

	queries := newQueryBatch(db)
	engine := dnsengine.New(blkStore, []string{"1.1.1.1", "9.9.9.9"}, queries.onQuery)

	var mgr *blocklist.Manager
	mgr = blocklist.NewManager(nil, cfg, blkStore, func() {
		emit("blocklist:status", mgr.Statuses())
	})

	// --- Startup repair (safety invariant, docs/SDA.md §4) ----------------------
	// If a previous run left system DNS pointing at us, serve immediately and
	// forward to the captured originals. If the port is busy we cannot repair
	// by serving — restore DNS instead so the machine is never blackholed.
	if state, ok, serr := sys.State(); serr != nil {
		log.Printf("repair: unreadable state file (manual fallback: resolvectl revert): %v", serr)
	} else if ok {
		applied, verr := sys.StillApplied(ctx)
		if verr != nil {
			log.Printf("repair: cannot verify system DNS (state kept): %v", verr)
		} else if !applied {
			log.Println("repair: system DNS no longer points at adbl, dropping stale state")
			sys.DiscardState()
		} else if err := engine.Start(state.Listen); err != nil {
			log.Printf("repair: cannot serve %s, restoring system DNS: %v", state.Listen, err)
			if rerr := sys.Restore(ctx); rerr != nil {
				log.Printf("repair: RESTORE FAILED — run manually: resolvectl revert (%v)", rerr)
			}
		} else {
			engine.SetUpstreams(state.Upstreams())
			log.Printf("repair: serving %s again (system DNS already points here)", state.Listen)
		}
	}
	if !engine.Running() {
		if err := engine.Start(listenAddr); err != nil {
			log.Printf("dns engine not started: %v", err)
		}
	}
	// Keep protection even if the app idles: refresh blocklists every 24h.
	mgr.StartAutoRefresh(ctx, 24*time.Hour)
	db.StartMaintenance(ctx)

	// --- Services ---------------------------------------------------------------
	dnsSvc := service.NewDNS(engine, listenAddr, emit)
	sysSvc := service.NewSystem(sys, dnsSvc, emit)
	statsSvc := service.NewStats(db, func() int { return len(mgr.Statuses()) })

	app = application.New(application.Options{
		Name:        "adbl",
		Description: "Local ad blocker, monitor and tracker dashboard",
		Services: []application.Service{
			application.NewService(dnsSvc),
			application.NewService(sysSvc),
			application.NewService(statsSvc),
			application.NewService(service.NewQueries(db)),
			application.NewService(service.NewBlocklists(mgr, blkStore)),
			application.NewService(service.NewRules(mgr, emit)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "adbl",
		Width:            1200,
		Height:           800,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(11, 15, 23),
		URL:              "/",
	})

	// --- Live event streams -----------------------------------------------------
	go queries.run(ctx, emit)
	go statsTicker(ctx, statsSvc, func() int { return len(mgr.Statuses()) }, emit)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}

	// --- Teardown: restore system DNS before anything else ----------------------
	stop()
	if _, ok, _ := sys.State(); ok {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := sys.Restore(rctx); err != nil {
			log.Printf("RESTORE FAILED — run manually: resolvectl revert (%v)", err)
		}
	}
}
