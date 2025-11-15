package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func reqID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}

		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = reqID()
		}
		ctx := r.Context()
		ctx = context.WithValue(ctx, "req_id", id)
		r = r.WithContext(ctx)

		next.ServeHTTP(sw, r)

		remoteIP := r.Header.Get("X-Forwarded-For")
		if remoteIP == "" {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			remoteIP = host
		}

		log.Info().
			Str("component", "http").
			Str("request_id", id).
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", sw.status).
			Str("user_agent", r.UserAgent()).
			Str("remote_ip", remoteIP).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("request completed")
	})
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value("req_id")
	log.Debug().
		Str("component", "app").
		Str("request_id", id.(string)).
		Msg("handling hello")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("hello from go-log-service\n"))
}

func workHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value("req_id").(string)
	task := r.URL.Query().Get("task")
	if task == "" {
		task = "simulate"
	}
	log.Info().Str("component", "worker").Str("request_id", id).Str("task", task).Msg("starting work")
	time.Sleep(150 * time.Millisecond)

	if strings.Contains(task, "fail") {
		log.Error().Str("component", "worker").Str("request_id", id).Str("task", task).Msg("work failed")
		http.Error(w, "failed", http.StatusInternalServerError)
		return
	}

	log.Warn().Str("component", "worker").Str("request_id", id).Str("task", task).Msg("work had minor issues")
	log.Info().Str("component", "worker").Str("request_id", id).Str("task", task).Msg("work done")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("healthy\n"))
}

func main() {
	// logging setup
	zerolog.TimeFieldFormat = time.RFC3339Nano
	level := zerolog.InfoLevel
	if lv := os.Getenv("LOG_LEVEL"); lv != "" {
		if parsed, err := zerolog.ParseLevel(lv); err == nil {
			level = parsed
		}
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)
	mux.HandleFunc("/work", workHandler)
	mux.HandleFunc("/healthz", healthHandler)

	srv := &http.Server{
		Addr:         addr,
		Handler:      logging(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info().Str("component", "server").Str("addr", addr).Msg("starting http server")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server error")
		}
	}()

	// graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info().Str("component", "server").Msg("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Info().Str("component", "server").Msg("bye")
}
