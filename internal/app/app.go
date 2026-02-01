package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hybrowse/hytale-session-token-broker/internal/broker"
	"github.com/hybrowse/hytale-session-token-broker/internal/config"
	"github.com/hybrowse/hytale-session-token-broker/internal/store"
)

type Dependencies struct {
	Now func() time.Time
}

func Main(ctx context.Context, argv []string, stdout io.Writer, stderr io.Writer, deps Dependencies) int {
	if deps.Now == nil {
		deps.Now = time.Now
	}

	fs := flag.NewFlagSet("hytale-session-token-broker", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	fs.StringVar(&configPath, "config", "/app/config.yaml", "path to config file")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	args := fs.Args()
	if len(args) == 0 {
		args = []string{"serve"}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}

	st := store.NewFileStore(cfg.Store.Path)
	br := broker.New(broker.Dependencies{
		Config: cfg,
		Store:  st,
		Now:    deps.Now,
	})

	var runErr error

	switch strings.ToLower(args[0]) {
	case "serve":
		runErr = br.Serve(ctx)
	case "auth-login-device":
		account := cfg.Defaults.Account
		if len(args) >= 2 {
			account = args[1]
		}
		runErr = br.AuthLoginDevice(ctx, stdout, account)
	case "auth-status":
		account := cfg.Defaults.Account
		if len(args) >= 2 {
			account = args[1]
		}
		runErr = br.AuthStatus(ctx, stdout, account)
	case "profiles":
		account := cfg.Defaults.Account
		if len(args) >= 2 {
			account = args[1]
		}
		runErr = br.PrintProfiles(ctx, stdout, account)
	case "set-profiles":
		if len(args) < 2 {
			runErr = errors.New("usage: set-profiles <uuid1,uuid2,...> [account]")
			break
		}
		list := strings.Split(args[1], ",")
		account := cfg.Defaults.Account
		if len(args) >= 3 {
			account = args[2]
		}
		runErr = br.SetDefaultProfiles(ctx, account, list)
	default:
		runErr = fmt.Errorf("unknown command: %s", args[0])
	}

	if runErr == nil {
		return 0
	}

	if errors.Is(runErr, context.Canceled) {
		return 0
	}

	_, _ = fmt.Fprintf(stderr, "error: %v\n", runErr)
	return 1
}
