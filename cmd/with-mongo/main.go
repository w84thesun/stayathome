package main

import (
	"context"
	"encoding/json"
	"expvar"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"go.mongodb.org/mongo-driver/bson/primitive"

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
	MongoURI             string `env:"MONGO_URI"`
}

type Test struct {
	Name string             `bson:"name"`
	Id   primitive.ObjectID `bson:"_id"`
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

	r := mux.NewRouter(mux.WithServiceName("stayathome"))
	server := http.Server{
		Addr:    net.JoinHostPort("", config.ServerPort),
		Handler: r,
	}

	mongo := New("test", config.MongoURI)
	err = mongo.Connect(context.Background())
	if err != nil {
		log.Panicw("failed to connect with mongo", "error", err)
	}

	diagLogger := log.With("subapp", "diag_router")
	diagRouter := mux.NewRouter(mux.WithServiceName("stayathome-diag"))
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
	r.HandleFunc("/get-data", func(
		w http.ResponseWriter, _ *http.Request) {
		log.Info("calling get-data request")
		result := mongo.Database.Collection("test").FindOne(context.Background(), bson.M{"name": "test"})
		if result.Err() != nil {
			log.Errorw("failed to get data", "error", result.Err())
		}

		t := &Test{}
		err = result.Decode(t)
		if err != nil {
			log.Errorw("failed to decode result", "error", err)
		}

		response, err := json.Marshal(t)
		if err != nil {
			log.Errorw("failed to marshal response", "error", err)
		}

		_, err = w.Write(response)
		if err != nil {
			log.Errorw("failed to write response", "error", err)
		}

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
