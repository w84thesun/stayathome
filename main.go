package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v6"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

type Config struct {
	ServerPort string `env:"SERVER_PORT,required"`
	DiagPort   string `env:"DIAG_PORT,required"`
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

	r := mux.NewRouter()
	server := http.Server{
		Addr:    net.JoinHostPort("", config.ServerPort),
		Handler: r,
	}

	diagRouter := mux.NewRouter()
	diagRouter.HandleFunc("/health", func(
		w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	diag := http.Server{
		Addr:    net.JoinHostPort("", config.DiagPort),
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

	err = diag.Shutdown(timeout)
	if err != nil {
		// ?
	}

	err = server.Shutdown(timeout)
	if err != nil {
		// ?
	}

	log.Info("application stopped")
}
