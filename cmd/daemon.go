package cmd

import (
	"context"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tsuru/kubernetes-router/api"
	"github.com/tsuru/kubernetes-router/backend"
	"github.com/tsuru/kubernetes-router/observability"
	"github.com/urfave/negroni"
)

type DaemonOpts struct {
	Name       string
	ListenAddr string
	Backend    backend.Backend
	KeyFile    string
	CertFile   string
}

func StartDaemon(opts DaemonOpts) {
	routerAPI := api.RouterAPI{
		Backend: opts.Backend,
	}

	r := mux.NewRouter().StrictSlash(true)

	r.PathPrefix("/api").Handler(negroni.New(
		api.AuthMiddleware{
			User: os.Getenv("ROUTER_API_USER"),
			Pass: os.Getenv("ROUTER_API_PASSWORD"),
		},
		negroni.Wrap(routerAPI.Routes()),
	))
	r.HandleFunc("/healthcheck", routerAPI.Healthcheck)
	r.Handle("/metrics", promhttp.Handler())

	r.HandleFunc("/debug/pprof/", pprof.Index)
	r.HandleFunc("/debug/pprof/heap", pprof.Index)
	r.HandleFunc("/debug/pprof/mutex", pprof.Index)
	r.HandleFunc("/debug/pprof/goroutine", pprof.Index)
	r.HandleFunc("/debug/pprof/threadcreate", pprof.Index)
	r.HandleFunc("/debug/pprof/block", pprof.Index)
	r.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	r.HandleFunc("/debug/pprof/profile", pprof.Profile)
	r.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	r.HandleFunc("/debug/pprof/trace", pprof.Trace)

	n := negroni.New(observability.Middleware(), negroni.NewLogger(), negroni.NewRecovery())
	n.UseHandler(r)

	server := http.Server{
		Addr:         opts.ListenAddr,
		Handler:      n,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go handleSignals(&server)

	if opts.KeyFile != "" && opts.CertFile != "" {
		log.Printf("Started listening and serving TLS at %s", opts.ListenAddr)
		if err := server.ListenAndServeTLS(opts.CertFile, opts.KeyFile); err != nil && err != http.ErrServerClosed {
			log.Fatalf("fail serve: %v", err)
		}
		return
	}
	log.Printf("Started listening and serving %s at %s", opts.Name, opts.ListenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("fail serve: %v", err)
	}
}

func handleSignals(server *http.Server) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT)
	sig := <-signals
	log.Printf("Received %s. Terminating...", sig)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	err := server.Shutdown(ctx)
	if err != nil {
		log.Fatalf("Error during server shutdown: %v", err)
	}
	log.Print("Server shutdown succeeded.")
}
