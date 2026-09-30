package main

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

const collnameJSON = `{"adlibJSON":{"recordList":[
 {"@priref":"1","collection":["TWL"],"institution":["ACOR"],"new_collection":["true"]},
 {"@priref":"4","collection":["TTE"],"institution":["ACOR"],"new_collection":["Yes"]},
 {"@priref":"5","collection":[{"@lang":"en-GB","value":"small finds"}],"institution":["Madaba Museum"]},
 {"@priref":"7","collection":["Empty Store"],"institution":["Jerash Museum"],"new_collection":["false"]}
],"diagnostic":{"hits":4}}}`

const facetJSON = `{"adlibJSON":{"recordList":[{"priref":["9"]}],
 "facetList":[{"facet":"collection.name","values":[
  {"term":"small finds","hits":1444,"priref":5},
  {"term":"TTE","hits":15,"priref":4},
  {"term":"TWL","hits":2,"priref":1},
  {"term":"Orphan","hits":3,"priref":99}]}],
 "diagnostic":{"hits":36580}}}`

// fakeAPI mimics wwwopac with record-level access control: data is only
// visible to a session that logged in with the right password.
func fakeAPI(t *testing.T, user, password string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method == http.MethodPost {
			r.ParseForm()
			q = r.PostForm
		}
		if r.URL.Query().Get("password") != "" {
			t.Errorf("password sent in the query string: %s", r.URL.RawQuery)
		}
		if q.Get("command") == "login" {
			if r.Method != http.MethodPost {
				t.Errorf("login sent as %s, want POST", r.Method)
			}
			if q.Get("username") == user && q.Get("password") == password {
				http.SetCookie(w, &http.Cookie{Name: "ASP.NET_SessionId", Value: "session-" + user})
				// Like the real WebAPI, a working login echoes nothing back.
				w.Write([]byte(`{"adlibJSON":{"recordList":[{"priref":0}],"diagnostic":{"hits":0}}}`))
				return
			}
			w.Write([]byte(`{"adlibJSON":{"recordList":[{"priref":0}],"diagnostic":{"hits":0}}}`))
			return
		}
		if c, err := r.Cookie("ASP.NET_SessionId"); err != nil || c.Value != "session-"+user {
			w.Write([]byte(`{"adlibJSON":{"diagnostic":{"hits":0}}}`))
			return
		}
		switch q.Get("database") {
		case "collname":
			w.Write([]byte(collnameJSON))
		case "collect":
			if q.Get("facets") != "collection.name" || q.Get("limit") == "0" {
				t.Errorf("unexpected collect query: %s", r.URL.RawQuery)
			}
			w.Write([]byte(facetJSON))
		default:
			w.Write([]byte(`{"adlibJSON":{"diagnostic":{"hits":0,"error":{"message":"Database 'x' could not be found"}}}}`))
		}
	}))
}

func newClient(srv *httptest.Server, user, password string) *Client {
	jar, _ := cookiejar.New(nil)
	hc := srv.Client()
	hc.Jar = jar
	return &Client{BaseURL: srv.URL, User: user, Password: password, HTTPClient: hc}
}

func TestRefreshExportsCollections(t *testing.T) {
	srv := fakeAPI(t, "metrics", "s3cret")
	defer srv.Close()
	c := newClient(srv, "metrics", "s3cret")
	r := NewRefresher(c, "collect", "collname", "collection.name", 5*time.Second)
	r.refresh(context.Background())

	want := `
# HELP axiell_collection_records Number of object records in each collection.
# TYPE axiell_collection_records gauge
axiell_collection_records{collection="Empty Store",institution="Jerash Museum",type="transfer"} 0
axiell_collection_records{collection="Orphan",institution="",type="transfer"} 3
axiell_collection_records{collection="TTE",institution="ACOR",type="new"} 15
axiell_collection_records{collection="TWL",institution="ACOR",type="new"} 2
axiell_collection_records{collection="small finds",institution="Madaba Museum",type="transfer"} 1444
# HELP axiell_records Total number of object records in the objects database.
# TYPE axiell_records gauge
axiell_records{database="collect"} 36580
# HELP axiell_up Whether the most recent refresh from the Axiell API succeeded.
# TYPE axiell_up gauge
axiell_up 1
`
	if err := testutil.CollectAndCompare(r, strings.NewReader(want), "axiell_collection_records", "axiell_records", "axiell_up"); err != nil {
		t.Fatal(err)
	}
}

func TestFailedRefreshKeepsLastSnapshot(t *testing.T) {
	srv := fakeAPI(t, "metrics", "s3cret")
	defer srv.Close()
	c := newClient(srv, "metrics", "s3cret")
	r := NewRefresher(c, "collect", "collname", "collection.name", 5*time.Second)
	r.refresh(context.Background())

	r.objectsDB = "missing"
	r.refresh(context.Background())
	if r.lastErr == nil || strings.Contains(r.lastErr.Error(), "s3cret") {
		t.Fatalf("want API error without the password, got %v", r.lastErr)
	}
	up := "# HELP axiell_up Whether the most recent refresh from the Axiell API succeeded.\n# TYPE axiell_up gauge\naxiell_up 0\n"
	if err := testutil.CollectAndCompare(r, strings.NewReader(up), "axiell_up"); err != nil {
		t.Fatal(err)
	}
	if n := testutil.CollectAndCount(r, "axiell_collection_records"); n != 5 {
		t.Fatalf("want last snapshot's 5 collections still exported, got %d", n)
	}
	if v := testutil.ToFloat64(r.refreshFailures); v != 1 {
		t.Fatalf("refresh failures = %v, want 1", v)
	}
}

func TestLoginWithoutRightsExportsNoCollections(t *testing.T) {
	srv := fakeAPI(t, "metrics", "s3cret")
	defer srv.Close()
	r := NewRefresher(newClient(srv, "metrics", "wrong"), "collect", "collname", "collection.name", 5*time.Second)
	r.refresh(context.Background())
	if r.lastErr != nil {
		t.Fatalf("refresh should succeed with no rights, got %v", r.lastErr)
	}
	if n := testutil.CollectAndCount(r, "axiell_collection_records"); n != 0 {
		t.Fatalf("want no collections exported, got %d", n)
	}
}

func TestAnonymousSkipsLogin(t *testing.T) {
	srv := fakeAPI(t, "metrics", "s3cret")
	defer srv.Close()
	r := NewRefresher(newClient(srv, "", ""), "collect", "collname", "collection.name", 5*time.Second)
	r.refresh(context.Background())
	if r.lastErr != nil {
		t.Fatalf("anonymous refresh should succeed with empty data, got %v", r.lastErr)
	}
}

func TestParseBool(t *testing.T) {
	for in, want := range map[string]bool{"true": true, "TRUE": true, "1": true, "yes": true, "": false, "false": false, "0": false} {
		if parseBool(in) != want {
			t.Errorf("parseBool(%q) = %v", in, !want)
		}
	}
}
