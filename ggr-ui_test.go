package main

import (
	"encoding/json"
	"fmt"
	. "github.com/aandryashin/matchers"
	. "github.com/aandryashin/matchers/httpresp"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
)

var (
	srv *httptest.Server
)

func init() {
	gitRevision = "test-revision"
	srv = httptest.NewServer(mux())
}

type With string

func (r With) Path(p string) string {
	return fmt.Sprintf("%s%s", r, p)
}

func TestPing(t *testing.T) {
	rsp, err := http.Get(With(srv.URL).Path("/ping"))

	AssertThat(t, err, Is{nil})
	AssertThat(t, rsp, Code{http.StatusOK})
	AssertThat(t, rsp.Body, Is{Not{nil}})

	var data map[string]interface{}
	bt, readErr := ioutil.ReadAll(rsp.Body)
	AssertThat(t, readErr, Is{nil})
	jsonErr := json.Unmarshal(bt, &data)
	AssertThat(t, jsonErr, Is{nil})
	_, hasUptime := data["uptime"]
	AssertThat(t, hasUptime, Is{true})
	version, hasVersion := data["version"]
	AssertThat(t, hasVersion, Is{true})
	AssertThat(t, version, EqualTo{"test-revision"})
}
