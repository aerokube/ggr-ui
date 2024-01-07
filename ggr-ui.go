package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/aerokube/ggr/config"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/websocket"
)

type Status map[string]interface{}

var paths = struct {
	Status  string
	VNC     string
	Logs    string
	Ping    string
	Metrics string
}{
	Status:  "/status",
	VNC:     "/vnc/",
	Logs:    "/logs/",
	Ping:    "/ping",
	Metrics: "/metrics",
}

func mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(paths.Status, status)
	mux.Handle(paths.VNC, websocket.Handler(proxyWS(paths.VNC)))
	mux.Handle(paths.Logs, websocket.Handler(proxyWS(paths.Logs)))
	mux.HandleFunc(paths.Ping, ping)
	mux.Handle(paths.Metrics, promhttp.Handler())
	return mux
}

type result struct {
	sum    string
	status Status
}

func status(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	defer lock.RUnlock()
	user, remote := info(r)
	quota, ok := userHosts(user)
	if !ok {
		log.Printf("[STATUS] [Unknown quota user: %s] [%s]", user, remote)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ch := make(chan struct{}, limit)
	rslt := make(chan *result)
	done := make(chan Status)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func(ctx context.Context, quota map[string]*config.Host) {
		for sum, h := range quota {
			select {
			case ch <- struct{}{}:
				go func(ctx context.Context, sum string, h *config.Host) {
					defer func() {
						<-ch
					}()
					r, err := http.NewRequest(http.MethodGet, h.Route()+paths.Status, nil)
					if err != nil {
						rslt <- nil
						log.Printf("[STATUS] [Failed to fetch status: %v] [%s]", err, remote)
						return
					}
					if h.Username != "" {
						r.SetBasicAuth(h.Username, h.Password)
					}
					ctx, cancel := context.WithTimeout(ctx, timeout)
					defer cancel()
					resp, err := http.DefaultClient.Do(r.WithContext(ctx))
					if err != nil {
						rslt <- nil
						log.Printf("[STATUS] [Failed to fetch status: %v] [%s]", err, remote)
						return
					}
					defer resp.Body.Close()
					m := make(map[string]interface{})
					err = json.NewDecoder(resp.Body).Decode(&m)
					if err != nil {
						rslt <- nil
						log.Printf("[STATUS] [Failed to parse response: %v] [%s]", err, remote)
						return
					}
					rslt <- &result{sum, m}
				}(ctx, sum, h)
			case <-ctx.Done():
				return
			}
		}
	}(ctx, quota)
	go func(ctx context.Context, quota map[string]*config.Host) {
		s := make(Status)
	loop:
		for i := 0; i < len(quota); i++ {
			select {
			case result := <-rslt:
				if result != nil && result.status != nil {
					s.Add(result.sum, result.status)
				}
			case <-time.After(responseTime):
				break loop
			case <-ctx.Done():
				return
			}
		}
		done <- s
	}(ctx, quota)
	select {
	case s := <-done:
		w.Header().Add("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	case <-r.Context().Done():
	}
}

func userHosts(user string) (map[string]*config.Host, bool) {
	if authenticatedAccessOnly {
		quota, ok := hosts[user]
		return quota, ok
	}

	ret := make(map[string]*config.Host)
	for _, quota := range hosts {
		for sum, host := range quota {
			ret[sum] = host
		}
	}
	return ret, true
}

func (cur Status) Add(sum string, m map[string]interface{}) {
	for k, v := range m {
		switch v.(type) {
		case float64:
			if curV, ok := cur[k].(float64); ok {
				cur[k] = v.(float64) + curV
			} else {
				cur[k] = v.(float64)
			}
		case []interface{}:
			for _, v := range v.([]interface{}) {
				if _, ok := v.(map[string]interface{}); ok {
					if id, ok := v.(map[string]interface{})["id"]; ok {
						v.(map[string]interface{})["id"] = sum + id.(string)
					}
				}
			}
			if _, ok := cur[k].([]interface{}); !ok {
				cur[k] = []interface{}{}
			}
			cur[k] = append(cur[k].([]interface{}), v.([]interface{})...)
		case map[string]interface{}:
			if _, ok := cur[k].(map[string]interface{}); !ok {
				cur[k] = make(map[string]interface{})
			}
			Status(cur[k].(map[string]interface{})).Add(sum, v.(map[string]interface{}))
		}
	}
}

func proxyWS(p string) func(wsconn *websocket.Conn) {
	return func(wsconn *websocket.Conn) {
		user, remote := info(wsconn.Request())
		log.Printf("[WEBSOCKET] [New connection] [%s]", remote)
		defer wsconn.Close()
		head := len(p)
		tail := head + md5.Size*2
		path := wsconn.Request().URL.Path
		if len(path) < tail {
			log.Printf("[WEBSOCKET] [Invalid websocket request: %s] [%s]", path, remote)
			return
		}
		sum := path[head:tail]
		lock.RLock()
		quota, ok := userHosts(user)
		lock.RUnlock()
		if !ok {
			log.Printf("[WEBSOCKET] [Unknown quota user: %s] [%s]", user, remote)
			return
		}
		lock.RLock()
		host, ok := quota[sum]
		lock.RUnlock()
		if !ok {
			log.Printf("[WEBSOCKET] [Unknown host sum: %s] [%s]", sum, remote)
			return
		}
		u, err := url.Parse(host.Route() + p + path[tail:])
		if err != nil {
			log.Printf("[WEBSOCKET] [Failed to parse url %s: %v] [%s]", u, err, remote)
			return
		}
		u.Scheme = "ws"
		log.Printf("[WEBSOCKET] [Starting websocket session to %s] [%s]", u, remote)
		conn, err := websocket.Dial(u.String(), "", "http://localhost")
		if err != nil {
			log.Printf("[WEBSOCKET] [Failed start websocket session to %s: %v] [%s]", u, err, remote)
			return
		}
		defer conn.Close()
		wsconn.PayloadType = websocket.BinaryFrame
		go func() {
			defer wsconn.Close()
			_, _ = io.Copy(wsconn, conn)
			log.Printf("[WEBSOCKET] [Closed websocket session to %s] [%s]", u, remote)
		}()
		_, _ = io.Copy(conn, wsconn)
		log.Printf("[WEBSOCKET] [Client disconnected: %s] [%s]", u, remote)
	}
}

func info(r *http.Request) (string, string) {
	remote := r.Header.Get("X-Forwarded-For")
	if remote == "" {
		remote, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	user := "unknown"
	if guestAccessAllowed {
		user = guestUserName
	} else {
		if u, _, ok := r.BasicAuth(); ok {
			user = u
		}
	}
	return user, remote
}

func ping(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Uptime  string `json:"uptime"`
		Version string `json:"version"`
	}{time.Since(startTime).String(), gitRevision})
}
