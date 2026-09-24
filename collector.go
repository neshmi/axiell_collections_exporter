package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "axiell"

// CollectionStat is the exported state of one collection.
type CollectionStat struct {
	Name        string
	Institution string
	Type        string // "new" or "transfer"
	Records     int
}

// Snapshot is the result of one successful refresh.
type Snapshot struct {
	TotalRecords int
	Collections  []CollectionStat
	Time         time.Time
}

// Refresher polls the API in the background so scrapes are served from memory
// and never wait on slow facet queries.
type Refresher struct {
	client          *Client
	objectsDB       string
	collectionsDB   string
	facetField      string
	timeout         time.Duration
	mu              sync.RWMutex
	snapshot        *Snapshot
	lastErr         error
	lastDuration    time.Duration
	refreshFailures prometheus.Counter
}

func NewRefresher(c *Client, objectsDB, collectionsDB, facetField string, timeout time.Duration) *Refresher {
	return &Refresher{
		client:        c,
		objectsDB:     objectsDB,
		collectionsDB: collectionsDB,
		facetField:    facetField,
		timeout:       timeout,
		refreshFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "refresh_failures_total",
			Help:      "Number of failed refreshes from the Axiell API.",
		}),
	}
}

func (r *Refresher) Run(ctx context.Context, interval time.Duration) {
	r.refresh(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.refresh(ctx)
		}
	}
}

func (r *Refresher) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()
	start := time.Now()
	snap, err := r.fetch(ctx)
	elapsed := time.Since(start)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastDuration = elapsed
	r.lastErr = err
	if err != nil {
		r.refreshFailures.Inc()
		log.Printf("refresh failed after %s: %v", elapsed.Round(time.Millisecond), err)
		return
	}
	r.snapshot = snap
	log.Printf("refreshed %d collections (%d records) in %s", len(snap.Collections), snap.TotalRecords, elapsed.Round(time.Millisecond))
}

func (r *Refresher) fetch(ctx context.Context) (*Snapshot, error) {
	var (
		wg                sync.WaitGroup
		colls             []Collection
		total             int
		facets            []FacetValue
		collErr, countErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); colls, collErr = r.client.Collections(ctx, r.collectionsDB) }()
	go func() {
		defer wg.Done()
		total, facets, countErr = r.client.CollectionCounts(ctx, r.objectsDB, r.facetField)
	}()
	wg.Wait()
	if countErr != nil {
		return nil, countErr
	}
	if collErr != nil {
		return nil, collErr
	}
	return &Snapshot{
		TotalRecords: total,
		Collections:  merge(colls, facets),
		Time:         time.Now(),
	}, nil
}

// merge joins the authority file with the facet counts. Every collection in
// the authority file is exported (at 0 if it has no records yet); facet terms
// with no authority record are exported as transfer so nothing is dropped.
func merge(colls []Collection, facets []FacetValue) []CollectionStat {
	byPriref := make(map[int]int, len(colls))
	byName := make(map[string]int, len(colls))
	stats := make([]CollectionStat, 0, len(colls))
	for _, c := range colls {
		if c.Name == "" {
			continue
		}
		key := strings.ToLower(c.Name)
		if i, dup := byName[key]; dup {
			// Same name twice: keep one series, prefer "new" if either says so.
			if c.New {
				stats[i].Type = "new"
			}
			byPriref[c.Priref] = i
			continue
		}
		i := len(stats)
		stats = append(stats, CollectionStat{Name: c.Name, Institution: c.Institution, Type: typeOf(c.New)})
		byName[key] = i
		if c.Priref != 0 {
			byPriref[c.Priref] = i
		}
	}
	for _, f := range facets {
		i, ok := byPriref[f.Priref]
		if !ok || f.Priref == 0 {
			i, ok = byName[strings.ToLower(f.Term)]
		}
		if !ok {
			i = len(stats)
			stats = append(stats, CollectionStat{Name: f.Term, Type: typeOf(false)})
			byName[strings.ToLower(f.Term)] = i
		}
		stats[i].Records += f.Hits
	}
	return stats
}

func typeOf(isNew bool) string {
	if isNew {
		return "new"
	}
	return "transfer"
}

var (
	collectionRecordsDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "collection", "records"),
		"Number of object records in each collection.",
		[]string{"collection", "institution", "type"}, nil)
	totalRecordsDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "records"),
		"Total number of object records in the objects database.",
		[]string{"database"}, nil)
	upDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "up"),
		"Whether the most recent refresh from the Axiell API succeeded.",
		nil, nil)
	lastSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "last_refresh_success", "timestamp_seconds"),
		"Unix time of the last successful refresh.",
		nil, nil)
	durationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "refresh", "duration_seconds"),
		"Duration of the most recent refresh.",
		nil, nil)
)

func (r *Refresher) Describe(ch chan<- *prometheus.Desc) {
	ch <- collectionRecordsDesc
	ch <- totalRecordsDesc
	ch <- upDesc
	ch <- lastSuccessDesc
	ch <- durationDesc
	r.refreshFailures.Describe(ch)
}

func (r *Refresher) Collect(ch chan<- prometheus.Metric) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	up := 0.0
	if r.lastErr == nil && r.snapshot != nil {
		up = 1
	}
	ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, up)
	ch <- prometheus.MustNewConstMetric(durationDesc, prometheus.GaugeValue, r.lastDuration.Seconds())
	r.refreshFailures.Collect(ch)

	// Keep serving the last good snapshot through API outages; axiell_up and
	// the last-success timestamp say how fresh it is.
	s := r.snapshot
	if s == nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(lastSuccessDesc, prometheus.GaugeValue, float64(s.Time.Unix()))
	ch <- prometheus.MustNewConstMetric(totalRecordsDesc, prometheus.GaugeValue, float64(s.TotalRecords), r.objectsDB)
	for _, c := range s.Collections {
		ch <- prometheus.MustNewConstMetric(collectionRecordsDesc, prometheus.GaugeValue,
			float64(c.Records), c.Name, c.Institution, c.Type)
	}
}
