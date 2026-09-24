package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	listenAddress   = flag.String("web.listen-address", ":9037", "Address to listen on for telemetry")
	metricsPath     = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics")
	baseURL         = flag.String("api.base-url", "http://localhost/api/wwwopac.ashx", "Base URL for the Axiell Collections wwwopac API")
	objectsDB       = flag.String("api.objects-database", "collect", "Database holding the object records")
	collectionsDB   = flag.String("api.collections-database", "collname", "Authority database listing the collections")
	facetField      = flag.String("api.collection-field", "collection.name", "Field on object records naming the collection")
	refreshInterval = flag.Duration("refresh.interval", 2*time.Minute, "How often to refresh counts from the API")
	refreshTimeout  = flag.Duration("refresh.timeout", 90*time.Second, "Timeout for one refresh")
)

func main() {
	flag.Parse()

	// Credentials come from the environment so they stay out of the process list.
	client := &Client{
		BaseURL:    *baseURL,
		User:       os.Getenv("AXIELL_USER"),
		Password:   os.Getenv("AXIELL_PASSWORD"),
		HTTPClient: &http.Client{Timeout: *refreshTimeout},
	}
	if client.User == "" {
		log.Printf("AXIELL_USER is not set; querying the API anonymously")
	}

	refresher := NewRefresher(client, *objectsDB, *collectionsDB, *facetField, *refreshTimeout)
	reg := prometheus.NewRegistry()
	reg.MustRegister(refresher, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go refresher.Run(ctx, *refreshInterval)

	mux := http.NewServeMux()
	mux.Handle(*metricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	srv := &http.Server{Addr: *listenAddress, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	log.Printf("axiell_collections_exporter listening on %s%s, API %s", *listenAddress, *metricsPath, *baseURL)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
