package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	listenAddress = flag.String("web.listen-address", ":9037", "Address to listen on for telemetry")
	metricsPath   = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics")
	baseUrl       = flag.String("api.base-url", "http://localhost/api/wwwopac.ashx", "Base URL for Axiell Collections API")
)

type Dataset struct {
	name string
}

type Databases struct {
	XMLName    xml.Name   `xml:"adlibXML"`
	RecordList RecordList `xml:"recordList"`
}

type RecordList struct {
	XMLName xml.Name `xml:"recordList"`
	Record  []Record `xml:"record"`
}

type Record struct {
	XMLName    xml.Name `xml:"record"`
	Database   string   `xml:"database"`
	Datasource string   `xml:"datasource"`
}

type AxiellDataset struct {
	metrics Metrics `json:"diaganostic"`
}

type Metrics struct {
	NumItems int `json:"hits"`
}

func init() {
	flag.Parse()
}

func main() {
	axiellCollector := NewAxiellCollector()
	prometheus.MustRegister(axiellCollector)
	log.Printf("Starting axiell_exporter on port %s", *listenAddress)
	log.Printf("Metrics path is %s", *metricsPath)
	log.Printf("Base URL is %s", *baseUrl)

	http.Handle(*metricsPath, promhttp.Handler())
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

// get database names from API endpoint
func getDatabases() ([]Dataset, error) {

	url := *baseUrl + "?command=listdatabases&output=xml"
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	var results Databases

	err = xml.NewDecoder(resp.Body).Decode(&results)
	if err != nil {
		return nil, err
	}

	var datasets []Dataset

	for _, record := range results.RecordList.Record {
		datasets = append(datasets, Dataset{record.Database})
	}

	return datasets, nil
}

func fetchNumItems(databaseName string) (int, error) {
	// Make an HTTP GET request to retrieve the number of items for a specific database
	safeDatabaseName := url.QueryEscape(databaseName)
	urlString := fmt.Sprintf("%s?database=%s&search=all", *baseUrl, safeDatabaseName)
	log.Printf("Fetching %s", urlString)
	resp, err := http.Get(urlString)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// Parse the JSON response and extract the number of items
	var metrics AxiellDataset
	err = json.NewDecoder(resp.Body).Decode(&metrics)
	if err != nil {
		return 0, err
	}

	return metrics.metrics.NumItems, nil
}

type axiellCollector struct {
	databaseNumItems *prometheus.Desc
}

func NewAxiellCollector() *axiellCollector {
	return &axiellCollector{
		databaseNumItems: prometheus.NewDesc(
			"dataset_items",
			"Number of items in each database",
			[]string{"dataset_name"},
			nil,
		),
	}
}

func (c *axiellCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.databaseNumItems
}

func (c *axiellCollector) Collect(ch chan<- prometheus.Metric) {
	// Fetch the list of databases
	databases, err := getDatabases()
	if err != nil {
		log.Println("Failed to fetch databases:", err)
		return
	}

	// Fetch the number of items for each database and export as Prometheus metric
	for _, database := range databases {
		numItems, err := fetchNumItems(database.name)
		if err != nil {
			log.Printf("Failed to fetch number of items for database %s: %v\n", database, err)
			continue
		}

		ch <- prometheus.MustNewConstMetric(
			c.databaseNumItems,
			prometheus.GaugeValue,
			float64(numItems),
			database.name,
		)
	}
}
