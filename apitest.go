package main

import (
	"encoding/xml"
	"fmt"
	"net/http"
)

type JSONResponse struct {
	adlibJSON AxiellDataset `json:"adlibJSON"`
	hits      int           `json:"adlibJSON.diagnostic.hits"`
}

type AxiellDataset struct {
	metrics Metrics `json:"diaganostic"`
}

type Metrics struct {
	NumItems int `json:"hits"`
}

func main() {
	url := "http://ni.acorjordan.org/api/wwwopac.ashx?database=collect&search=all" // Replace with your API endpoint URL.

	resp, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var jsonresponse JSONResponse

	err = xml.NewDecoder(resp.Body).Decode(&jsonresponse)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Number of items: %d\n", jsonresponse)

}
