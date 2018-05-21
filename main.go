package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"encoding/xml"
	"github.com/aerokube/ggr/config"
	"io/ioutil"
	"sync"
)

var (
	lock    sync.RWMutex
	hosts   = make(map[string]string)
	limitCh chan struct{}
)

var (
	listen      string
	limit       int
	quotaDir    string
	gracePeriod time.Duration

	version     bool
	gitRevision = "HEAD"
	buildStamp  = "unknown"

	startTime = time.Now()
)

func configure() error {
	glob := fmt.Sprintf("%s%c%s", quotaDir, filepath.Separator, "*.xml")
	files, _ := filepath.Glob(glob)
	if len(files) == 0 {
		return fmt.Errorf("no quota XML files found in %s", quotaDir)
	}
	for _, fn := range files {
		file, err := ioutil.ReadFile(fn)
		if err != nil {
			log.Printf("[INIT] [Error reading configuration file %s: %v]", fn, err)
			continue
		}
		var browsers config.Browsers
		if err := xml.Unmarshal(file, &browsers); err != nil {
			log.Printf("[INIT] [Error parsing configuration file %s: %v]", fn, err)
			continue
		}
		for _, b := range browsers.Browsers {
			for _, v := range b.Versions {
				for _, r := range v.Regions {
					for _, h := range r.Hosts {
						url := fmt.Sprintf("http://%s", net.JoinHostPort(h.Name, strconv.Itoa(h.Port)))
						lock.Lock()
						hosts[h.Sum()] = url
						lock.Unlock()
					}
				}
			}
		}
	}
	return nil
}

func init() {
	flag.StringVar(&listen, "listen", ":8888", "host and port to listen to")
	flag.IntVar(&limit, "limit", 10, "simultaneous http requests")
	flag.StringVar(&quotaDir, "quota-dir", "quota", "quota directory")
	flag.DurationVar(&gracePeriod, "grace-period", 300*time.Second, "graceful shutdown period")
	flag.BoolVar(&version, "version", false, "Show version and exit")
	flag.Parse()

	if version {
		showVersion()
		os.Exit(0)
	}

	limitCh = make(chan struct{}, limit)
	err := configure()
	if err != nil {
		log.Fatalf("[INIT] [Failed to load quota files: %v]", err)
	}

	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGHUP)
	go func() {
		for {
			<-sig
			configure()
		}
	}()
}

func main() {
	limitCh = make(chan struct{}, limit)
	stop := make(chan os.Signal)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	server := &http.Server{
		Addr:    listen,
		Handler: mux(),
	}
	e := make(chan error)
	go func() {
		e <- server.ListenAndServe()
	}()
	select {
	case err := <-e:
		log.Fatalf("[INIT] [Failed to start http server: %v]", err)
	case <-stop:
	}

	log.Printf("[SHUTDOWN] [Shutting down in %v]", gracePeriod)
	ctx, cancel := context.WithTimeout(context.Background(), gracePeriod)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("[SHUTDOWN] [Failed to shut down: %v]", err)
	}
}

func showVersion() {
	fmt.Printf("Git Revision: %s\n", gitRevision)
	fmt.Printf("UTC Build Time: %s\n", buildStamp)
}
