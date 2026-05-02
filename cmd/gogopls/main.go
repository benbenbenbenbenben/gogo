package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/benbenbenbenbenben/gogo/internal/gogopls"
)

const defaultSyncInterval = 2 * time.Second

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatalf("cwd: %v", err)
	}

	syncer := gogopls.NewSyncer(root)
	if err := syncer.Sync(); err != nil {
		log.Printf("gogopls: initial sync: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(syncInterval())
		defer ticker.Stop()
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := syncer.Sync(); err != nil {
					log.Printf("gogopls: workspace sync: %v", err)
				}
			}
		}
	}()

	cmd := exec.CommandContext(ctx, "gopls", os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	stop()
	<-done

	if cleanupErr := syncer.Cleanup(); cleanupErr != nil {
		log.Printf("gogopls: cleanup: %v", cleanupErr)
	}

	if err == nil {
		return
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	fatalf("gopls: %v", err)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func syncInterval() time.Duration {
	value := os.Getenv("GOGOPLS_SYNC_INTERVAL")
	if value == "" {
		return defaultSyncInterval
	}

	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		fatalf("invalid GOGOPLS_SYNC_INTERVAL %q (expected values like 1s or 2500ms)", value)
	}
	return d
}
