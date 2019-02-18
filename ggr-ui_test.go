package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func init() {
	gitRevision = "test-revision"
}

type Selenoid struct {
	*httptest.Server
	Host string
	Sum  string
}

func NewSelenoid(mux http.Handler) *Selenoid {
	s := httptest.NewServer(mux)
	u, _ := url.Parse(s.URL)
	return &Selenoid{
		Server: s,
		Host:   u.Host,
		Sum:    fmt.Sprintf("%x", md5.Sum([]byte(s.URL))),
	}
}

type GGR struct {
	*httptest.Server
}

func NewGGR() *GGR {
	return &GGR{httptest.NewServer(mux())}
}

func CheckPath(p string) (*url.URL, error) {
	u, err := url.ParseRequestURI(p)
	if err != nil {
		return nil, fmt.Errorf("check path: %v", err)
	}
	return u, nil
}

func ReadResponseBody(r io.Reader) ([]byte, error) {
	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response body: %v", err)
	}
	return buf, nil
}

func CheckResponseCode(code int) error {
	if code != http.StatusOK {
		return fmt.Errorf("bad response code: %s", http.StatusText(code))
	}
	return nil
}

func DecodeResponse(r []byte) (map[string]interface{}, error) {
	var v map[string]interface{}
	err := json.Unmarshal(r, &v)
	if err != nil {
		return nil, fmt.Errorf("decode response: %v", err)
	}
	return v, nil
}

func QueryError(err error, p string) error {
	return fmt.Errorf("query %s: %v", p, err)
}

func (ggr *GGR) Query(ctx context.Context, p string) (map[string]interface{}, error) {
	u, err := CheckPath(p)
	if err != nil {
		return nil, QueryError(err, p)
	}
	if err != nil {
		return nil, QueryError(err, p)
	}
	req, err := http.NewRequest(http.MethodGet, ggr.URL+u.Path, nil)
	if err != nil {
		return nil, QueryError(err, p)
	}
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, QueryError(err, p)
	}
	err = CheckResponseCode(resp.StatusCode)
	if err != nil {
		return nil, QueryError(err, p)
	}
	buf, err := ReadResponseBody(resp.Body)
	if err != nil {
		return nil, QueryError(err, p)
	}
	r, err := DecodeResponse(buf)
	if err != nil {
		return nil, QueryError(err, p)
	}
	return r, nil
}

func (ggr *GGR) Status(ctx context.Context) (Status, error) {
	r, err := ggr.Query(ctx, "/status")
	if err != nil {
		return nil, fmt.Errorf("status: %v", err)
	}
	return Status(r), nil
}

func (ggr *GGR) Ping(ctx context.Context) (map[string]interface{}, error) {
	r, err := ggr.Query(ctx, "/ping")
	if err != nil {
		return nil, fmt.Errorf("ping: %v", err)
	}
	return r, nil
}

func TestPing(t *testing.T) {
	ggr := NewGGR()
	defer ggr.Close()

	data, err := ggr.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, ok := data["uptime"]
	if !ok {
		t.Error("there is no uptime")
	}
	version := data["version"]
	if version != "test-revision" {
		t.Errorf("version\n")
	}
}

func TestBrokenConfig(t *testing.T) {
	m := map[string]string{
		"md5sum": "://localhost",
	}
	lock.Lock()
	hosts = m
	lock.Unlock()
	ggr := NewGGR()
	defer ggr.Close()
	s, err := ggr.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 0 {
		t.Errorf("uexpected nonempty status: %v\n", s)
	}
}

func TestQueue(t *testing.T) {
	tempLimit := limit
	limit = 0
	defer func(l int) {
		limit = l
	}(tempLimit)
	tempTimeout := timeout
	timeout = 100 * time.Millisecond
	defer func(t time.Duration) {
		timeout = t
	}(tempTimeout)
	ggr := NewGGR()
	defer ggr.Close()
	s, err := ggr.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 0 {
		t.Errorf("uexpected nonempty status: %v\n", s)
	}
}

func TestResponseTime(t *testing.T) {
	temp := responseTime
	responseTime = 100 * time.Millisecond
	defer func(tmp time.Duration) {
		responseTime = tmp
	}(temp)
	selenoid := NewSelenoid(silent)
	m := map[string]string{
		selenoid.Sum: selenoid.URL,
	}
	lock.Lock()
	hosts = m
	lock.Unlock()

	ggr := NewGGR()
	defer ggr.Close()
	s, err := ggr.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 0 {
		t.Errorf("uexpected nonempty status: %v\n", s)
	}
}

var (
	silent = http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	empty = http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total":1,"used":0,"queued":0,"pending":0,"browsers":{"chrome":{"60.0":{}},"firefox":{"59.0":{}}}}`)
	}))
	broken = http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total":1,"used":0,"queued":0,"pending":0,`)
	}))
)

type Case struct {
	Name           string
	Expected       float64
	Timeout        time.Duration
	ContextTimeout time.Duration
	Handlers       []http.Handler
}

func TestStatus(t *testing.T) {
	cases := []Case{
		Case{
			Name:     "ConnectionRefused",
			Expected: 0,
			Timeout:  100 * time.Millisecond,
			Handlers: []http.Handler{nil, nil, nil},
		},
		Case{
			Name:     "Timeout",
			Expected: 0,
			Timeout:  100 * time.Millisecond,
			Handlers: []http.Handler{silent},
		},
		Case{
			Name:           "ClientDisconnected",
			Expected:       0,
			ContextTimeout: 100 * time.Millisecond,
			Handlers:       []http.Handler{silent},
		},
		Case{
			Name:     "TwoHostsDown",
			Expected: 1,
			Handlers: []http.Handler{nil, nil, empty},
		},
		Case{
			Name:     "TwoHostsBroken",
			Expected: 1,
			Handlers: []http.Handler{broken, broken, empty},
		},
		Case{
			Name:     "TwoHostsNoAnswer",
			Expected: 1,
			Timeout:  100 * time.Millisecond,
			Handlers: []http.Handler{silent, silent, empty},
		},
		Case{
			Name:     "AllHostsUpAndRunning",
			Expected: 3,
			Handlers: []http.Handler{empty, empty, empty},
		},
	}
	for i, c := range cases {
		m := map[string]string{}
		for _, handler := range c.Handlers {
			selenoid := NewSelenoid(handler)
			if handler != nil {
				defer selenoid.Close()
			} else {
				selenoid.Close()
			}
			m[selenoid.Sum] = selenoid.URL
		}
		lock.Lock()
		hosts = m
		lock.Unlock()
		name := strconv.Itoa(i)
		if c.Name != "" {
			name = c.Name
		}
		t.Run(name, func(t *testing.T) {
			if c.Timeout != 0 {
				temp := timeout
				timeout = c.Timeout
				defer func(t time.Duration) {
					timeout = t
				}(temp)
			}
			ggr := NewGGR()
			defer ggr.Close()
			expectTimeout := false
			ctx := context.Background()
			if c.ContextTimeout != 0 {
				expectTimeout = true
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, c.ContextTimeout)
				defer cancel()
			}
			s, err := ggr.Status(ctx)
			if expectTimeout && err == nil {
				t.Fatal("unexpected success")
			}
			if !expectTimeout && err != nil {
				t.Fatal(err)
			}
			if c.Expected != 0 {
				if s["total"] != c.Expected {
					t.Errorf("uexpected total: %g\n", s["total"])
				}
			} else {
				if len(s) != 0 {
					t.Errorf("uexpected nonempty status: %v\n", s)
				}
			}
		})
	}
}
