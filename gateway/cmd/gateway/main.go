// Command gateway is the MedVault API Gateway: a thin REST layer that
// translates HTTP requests into VaultDB SQL and serves the React frontend.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"medvault-gateway/internal/api"
	"medvault-gateway/internal/auth"
	"medvault-gateway/internal/vaultdb"
)

var version = "1.0.2"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := envStr("PORT", "4000")
	vaultHost := envStr("VAULTDB_HOST", "vaultdb")
	vaultPort := envStr("VAULTDB_PORT", "5432")
	vaultMonitorPort := envStr("VAULTDB_MONITOR_PORT", "5433")
	database := envStr("MEDVAULT_DB", "medvault")
	jwtSecret := envStr("JWT_SECRET", "medvault_dev_secret_change_me")
	jwtTTL := envInt("JWT_TTL_HOURS", 24)

	addr := fmt.Sprintf("%s:%s", vaultHost, vaultPort)
	monitorURL := fmt.Sprintf("http://%s:%s", vaultHost, vaultMonitorPort)

	db := vaultdb.New(addr, database)
	signer := auth.NewSigner(jwtSecret, time.Duration(jwtTTL)*time.Hour)
	handler := api.New(db, signer, monitorURL)

	// Wait for VaultDB + database to become reachable (seed may still be running).
	waitForDB(db, logger)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("gateway listening", "version", version, "port", port, "vaultdb", addr, "database", database)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	logger.Info("gateway shutdown complete")
}

// waitForDB retries Ping until VaultDB answers and the database is selectable.
// This tolerates the gateway starting before the seed job creates the database.
func waitForDB(db *vaultdb.Client, logger *slog.Logger) {
	for i := 0; i < 60; i++ {
		if err := db.Ping(); err == nil {
			logger.Info("vaultdb reachable")
			return
		} else {
			logger.Info("waiting for vaultdb/database", "attempt", i+1, "error", err.Error())
		}
		time.Sleep(2 * time.Second)
	}
	logger.Warn("proceeding without confirmed vaultdb connection")
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
