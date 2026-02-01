package main

import (
	"context"
	"os"

	"github.com/hybrowse/hytale-session-token-broker/internal/app"
)

func main() {
	code := app.Main(context.Background(), os.Args[1:], os.Stdout, os.Stderr, app.Dependencies{})
	os.Exit(code)
}
