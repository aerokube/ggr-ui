package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/aerokube/util"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/websocket"
)

type Status map[string]interface{}

var paths = struct {
	Status string
	VNC    string
	Logs   string
	Ping   string
}{
	Status: "/status",
	VNC:    "/vnc/",
	Logs:   "/logs/",
	Ping:   "/ping",
}

func mux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc(paths.Status, status)
	mux.Handle(paths.VNC, websocket.Handler(proxyWS(paths.VNC)))
	mux.Handle(paths.Logs, websocket.Handler(proxyWS(paths.Logs)))
	mux.HandleFunc(paths.Ping, ping)
	return mux
}

func status(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	defer lock.RUnlock()
	_, remote := util.RequestInfo(r)
	ch := make(chan struct{}, limit)
	rslt := make(chan Status)
	for _, u := range hosts {
		ch <- struct{}{}
		go func(u string) {
			defer func() {
				<-ch
			}()
			r, err := http.NewRequest(http.MethodGet, u+paths.Status, nil)
			if err != nil {
				rslt <- nil
				log.Printf("[STATUS] [Failed to fetch status from %s: %v] [%s]", u, err, remote)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			resp, err := http.DefaultClient.Do(r.WithContext(ctx))
			if err != nil {
				rslt <- nil
				log.Printf("[STATUS] [Failed to fetch status from %s: %v] [%s]", u, err, remote)
				return
			}
			defer resp.Body.Close()
			m := make(map[string]interface{})
			err = json.NewDecoder(resp.Body).Decode(&m)
			if err != nil {
				rslt <- nil
				log.Printf("[STATUS] [Failed to parse response from %s: %v] [%s]", u, err, remote)
				return
			}
			rslt <- m
		}(u)
	}
	s := make(Status)
	for sum, _ := range hosts {
		if m := <-rslt; m != nil {
			s.Add(sum, m)
		}
	}
	json.NewEncoder(w).Encode(s)
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
		_, remote := util.RequestInfo(wsconn.Request())
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
		host, ok := hosts[sum]
		lock.RUnlock()
		if !ok {
			log.Printf("[WEBSOCKET] [Unknown host sum: %s] [%s]", sum, remote)
			return
		}
		u, err := url.Parse(host + p + path[tail:])
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
			io.Copy(wsconn, conn)
			wsconn.Close()
			log.Printf("[WEBSOCKET] [Closed websocket session to %s] [%s]", u, remote)
		}()
		io.Copy(conn, wsconn)
		log.Printf("[WEBSOCKET] [Client disconnected: %s] [%s]", u, remote)
	}
}

func ping(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Uptime  string `json:"uptime"`
		Version string `json:"version"`
	}{time.Since(startTime).String(), gitRevision})
}
