package main

import (
	"context"
	"expvar"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/caarlos0/env/v6"
	"go.uber.org/zap"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Config struct {
	ServerPort           string `env:"SERVER_PORT,required"`
	DiagnosticServerPort string `env:"DIAG_PORT,required"`
	StatsdPort           string `env:"STATSD_PORT,required"`
	DataServiceURL       string `env:"DATA_SERVICE_URL,required"`
}

func main() {
	config := &Config{}

	err := env.Parse(config)
	if err != nil {
		panic(err)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()
	log := logger.Sugar()

	log.Info("starting application")

	// start the tracer with zero or more options
	tracer.Start(tracer.WithServiceName("stayathome"))
	defer tracer.Stop()

	c, err := statsd.New(net.JoinHostPort("127.0.0.1", config.StatsdPort))
	if err != nil {
		log.Fatal(err)
	}
	c.Namespace = "stayathome."

	r := mux.NewRouter(mux.WithServiceName("stayathome-2"))
	server := http.Server{
		Addr:    net.JoinHostPort("", config.ServerPort),
		Handler: r,
	}
	r.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		log.Info("handling get request")

		cli := http.Client{}
		result, err := cli.Get(config.DataServiceURL)
		if err != nil {
			log.Errorw("get request failed", "error", err)
		}

		response := make([]byte, 0)
		_, err = io.ReadFull(result.Body, response)
		if err != nil {
			log.Errorw("failed to read response", "error", err)
		}

		_, err = w.Write(response)
		if err != nil {
			log.Errorw("failed to write response", "error", err)
		}

		w.WriteHeader(http.StatusOK)
	})

	diagLogger := log.With("subapp", "diag_router")
	diagRouter := mux.NewRouter(mux.WithServiceName("stayathome-2-diag"))
	diagRouter.Handle("/debug/vars", expvar.Handler())
	diagRouter.HandleFunc("/health", func(
		w http.ResponseWriter, _ *http.Request) {
		err := c.Incr("health_calls", []string{}, 1.0)
		if err != nil {
			diagLogger.Errorw("failed to increment health_calls", "err", err)
		}
		diagLogger.Info("health called")
		w.WriteHeader(http.StatusOK)
	})
	diagRouter.HandleFunc("/gc", func(
		w http.ResponseWriter, _ *http.Request) {
		diagLogger.Info("calling GC")
		runtime.GC()
		w.WriteHeader(http.StatusOK)
	})

	diag := http.Server{
		Addr:    net.JoinHostPort("", config.DiagnosticServerPort),
		Handler: diagRouter,
	}

	shutdown := make(chan error, 2)

	go func() {
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			shutdown <- err
		}
	}()

	go func() {
		err := diag.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			shutdown <- err
		}
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case x := <-interrupt:
		// Received a signal
		log.Infof("got interrupt signal, exiting: %v", x)

	case err := <-shutdown:
		// Received a shutdown message
		log.Errorf("server err received: %v", err)
	}

	timeout, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	err = server.Shutdown(timeout)
	if err != nil {
		log.Errorw("business server shutdown error", "error", err)
	}

	err = diag.Shutdown(timeout)
	if err != nil {
		log.Errorw("diagnostics server shutdown error", "error", err)
	}

	log.Info("application stopped")
}
