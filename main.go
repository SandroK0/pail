package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-objectstore/config"
	"go-objectstore/internal/adminapi"
	"go-objectstore/internal/events"
	"go-objectstore/internal/s3api"
	"go-objectstore/internal/server"
	"go-objectstore/internal/store"
	"go-objectstore/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var logOut io.Writer = os.Stderr
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer f.Close()
		logOut = io.MultiWriter(os.Stderr, f)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logOut, nil)))

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store %q: %w", cfg.DataDir, err)
	}
	defer st.Close()
	for _, b := range cfg.Buckets {
		if err := st.CreateBucket(context.Background(), b); err != nil && !errors.Is(err, store.ErrBucketExists) {
			return fmt.Errorf("create bucket %q: %w", b, err)
		}
	}

	broker := events.NewBroker(1000)
	s3 := s3api.New(st, s3api.Config{
		Region:            cfg.Region,
		AccessKey:         cfg.AccessKey,
		SecretKey:         cfg.SecretKey,
		VerifyAuth:        cfg.VerifyAuth,
		AutoCreateBuckets: cfg.AutoCreateBuckets,
		CORSOrigins:       cfg.CORSOrigins,
	}, broker)
	admin := adminapi.New(st, broker, adminapi.Config{
		PublicURL:  cfg.PublicURL,
		Region:     cfg.Region,
		AccessKey:  cfg.AccessKey,
		SecretKey:  cfg.SecretKey,
		VerifyAuth: cfg.VerifyAuth,
		Buckets:    cfg.Buckets,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(s3, admin, web.FS()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	printBanner(cfg)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func printBanner(cfg config.Config) {
	bucket := "laravel"
	if len(cfg.Buckets) > 0 {
		bucket = cfg.Buckets[0]
	}
	auth := "off (any credentials accepted)"
	if cfg.VerifyAuth {
		auth = "verify"
	}
	fmt.Printf(`
  go-objectstore is running

  UI        %[1]s/_ui/
  S3        %[1]s
  Data      %[2]s
  Auth      %[3]s

  Laravel .env:
    FILESYSTEM_DISK=s3
    AWS_ACCESS_KEY_ID=%[4]s
    AWS_SECRET_ACCESS_KEY=%[5]s
    AWS_DEFAULT_REGION=%[6]s
    AWS_BUCKET=%[7]s
    AWS_ENDPOINT=%[1]s
    AWS_USE_PATH_STYLE_ENDPOINT=true
    AWS_URL=%[1]s/%[7]s

`, cfg.PublicURL, cfg.DataDir, auth, cfg.AccessKey, cfg.SecretKey, cfg.Region, bucket)
}
