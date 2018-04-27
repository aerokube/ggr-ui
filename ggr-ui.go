package main

import (
	"crypto/md5"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"

	"golang.org/x/net/websocket"
)

type Status map[string]interface{}

var paths = struct {
	Status string
	VNC    string
	Logs   string
}{
	Status: "/status",
	VNC:    "/vnc/",
	Logs:   "/logs/",
}

func mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(paths.Status, status)
	mux.Handle(paths.VNC, websocket.Handler(proxyWS(paths.VNC)))
	mux.Handle(paths.Logs, websocket.Handler(proxyWS(paths.Logs)))
	return mux
}

func status(w http.ResponseWriter, r *http.Request) {
	s := make(Status)
	for sum, url := range hosts {
		resp, err := http.Get(url + paths.Status)
		if err != nil {
			log.Printf("quering %s: %v", url, err)
			continue
		}
		defer resp.Body.Close()
		m := make(map[string]interface{})
		err = json.NewDecoder(resp.Body).Decode(&m)
		if err != nil {
			log.Printf("parsing response from %s: %v", url, err)
			continue
		}
		s.Add(sum, m)
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
			if _, ok := cur[k].([]interface{}); ok {
				cur[k] = append(cur[k].([]interface{}), v.([]interface{})...)
			} else {
				cur[k] = append([]interface{}{}, v.([]interface{})...)
			}
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
		log.Printf("new ws connection\n")
		defer wsconn.Close()
		head := len(p)
		tail := head + md5.Size*2
		path := wsconn.Request().URL.Path
		if len(path) < tail {
			log.Printf("invalid ws request: %s\n", path)
			return
		}
		sum := path[head:tail]
		host, ok := hosts[sum]
		if !ok {
			log.Printf("unknown host\n")
			return
		}
		u, err := url.Parse(host + p + path[tail:])
		if err != nil {
			log.Printf("parse url %s: %v\n", u, err)
			return
		}
		u.Scheme = "ws"
		log.Printf("start ws session to %s", u)
		conn, err := websocket.Dial(u.String(), "", "http://localhost")
		if err != nil {
			log.Printf("start ws session: %v", err)
			return
		}
		defer conn.Close()
		wsconn.PayloadType = websocket.BinaryFrame
		go func() {
			io.Copy(wsconn, conn)
			wsconn.Close()
			log.Printf("ws session closed %s", u)
		}()
		io.Copy(conn, wsconn)
		log.Printf("ws client disconnected %s", u)
	}
}
