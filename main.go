package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Metrics struct {
	NumItems int    `json:"hits"`
	query    string `json:"search"`
}
type AxiellDataset struct {
	metrics Metrics `json:"diaganostic"`
}
type JSONResponse struct {
	adlibJSON AxiellDataset `json:"adlibJSON"`
}

func main() {
	url := "http://192.168.30.52/api/wwwopac.ashx?database=collect&search=all" // Replace with your API endpoint URL.

	resp, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var jsonresponse JSONResponse
	err = json.NewDecoder(resp.Body).Decode(&jsonresponse)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%+v\n", jsonresponse)
}
