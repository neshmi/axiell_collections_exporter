package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client talks to the Axiell Collections wwwopac API.
type Client struct {
	BaseURL    string
	User       string
	Password   string
	HTTPClient *http.Client
}

// Collection is one entry in the collname authority file.
type Collection struct {
	Priref      int
	Name        string
	Institution string
	New         bool
}

// FacetValue is one collection.name facet bucket from the collect database.
type FacetValue struct {
	Term   string
	Hits   int
	Priref int
}

type diagnostic struct {
	Hits  int `json:"hits"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type response struct {
	AdlibJSON struct {
		Diagnostic diagnostic                   `json:"diagnostic"`
		RecordList []map[string]json.RawMessage `json:"recordList"`
		FacetList  []struct {
			Facet  string `json:"facet"`
			Values []struct {
				Term   string `json:"term"`
				Hits   int    `json:"hits"`
				Priref int    `json:"priref"`
			} `json:"values"`
		} `json:"facetList"`
	} `json:"adlibJSON"`
}

func (c *Client) get(ctx context.Context, params url.Values) (*response, error) {
	params.Set("output", "json")
	if c.User != "" {
		params.Set("user", c.User)
		params.Set("password", c.Password)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// Never surface the URL: it carries the password.
		return nil, fmt.Errorf("request to database %q failed: %w", params.Get("database"), unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("database %q: HTTP %d", params.Get("database"), resp.StatusCode)
	}
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("database %q: decoding response: %w", params.Get("database"), err)
	}
	// wwwopac reports errors with HTTP 200 and a diagnostic.error block.
	if e := r.AdlibJSON.Diagnostic.Error; e != nil {
		return nil, fmt.Errorf("database %q: API error: %s", params.Get("database"), e.Message)
	}
	return &r, nil
}

func unwrapURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

// CollectionCounts returns the total number of records in the objects database
// and the per-collection record counts from a single facet query.
func (c *Client) CollectionCounts(ctx context.Context, database, facetField string) (int, []FacetValue, error) {
	r, err := c.get(ctx, url.Values{
		"database": {database},
		"search":   {"all"},
		// limit=0 can stall the API for many seconds; one record is cheap.
		"limit":  {"1"},
		"facets": {facetField},
	})
	if err != nil {
		return 0, nil, err
	}
	var values []FacetValue
	for _, f := range r.AdlibJSON.FacetList {
		if f.Facet != facetField {
			continue
		}
		for _, v := range f.Values {
			values = append(values, FacetValue{Term: v.Term, Hits: v.Hits, Priref: v.Priref})
		}
	}
	return r.AdlibJSON.Diagnostic.Hits, values, nil
}

// Collections lists every record in the collname authority file.
func (c *Client) Collections(ctx context.Context, database string) ([]Collection, error) {
	const pageSize = 500
	var out []Collection
	for start := 1; ; start += pageSize {
		r, err := c.get(ctx, url.Values{
			"database":  {database},
			"search":    {"all"},
			"limit":     {strconv.Itoa(pageSize)},
			"startfrom": {strconv.Itoa(start)},
		})
		if err != nil {
			return nil, err
		}
		for _, rec := range r.AdlibJSON.RecordList {
			priref, _ := strconv.Atoi(firstString(rec["@priref"]))
			if priref == 0 {
				priref, _ = strconv.Atoi(firstString(rec["priref"]))
			}
			out = append(out, Collection{
				Priref:      priref,
				Name:        firstString(rec["collection"]),
				Institution: firstString(rec["institution"]),
				New:         parseBool(firstString(rec["new_collection"])),
			})
		}
		if len(r.AdlibJSON.RecordList) < pageSize || start+pageSize > r.AdlibJSON.Diagnostic.Hits {
			return out, nil
		}
	}
}

// firstString pulls the first scalar out of a grouped-JSON field, which may be
// a plain value, an array of values, or language-tagged objects.
func firstString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return scalar(v)
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		for _, e := range t {
			if s := scalar(e); s != "" {
				return s
			}
		}
	case map[string]any:
		for _, k := range []string{"value", "#text", "spans", "text"} {
			if e, ok := t[k]; ok {
				if s := scalar(e); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// parseBool accepts the values Adlib may write for a Logical field.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y", "x", "on":
		return true
	}
	return false
}
