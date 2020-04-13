package main

import (
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aerokube/ggr/config"
)

var (
	lock  sync.RWMutex
	hosts map[string]map[string]*config.Host
)

var (
	listen             string
	timeout            time.Duration
	responseTime       time.Duration
	limit              int
	quotaDir           string
	gracePeriod        time.Duration
	guestAccessAllowed bool
	guestUserName      string

	version     bool
	gitRevision = "HEAD"
	buildStamp  = "unknown"

	startTime = time.Now()
)

func configure() error {
	log.Printf("[INIT] [Loading quota files from %s]", quotaDir)
	pref, suff := fmt.Sprintf("%s%c", quotaDir, filepath.Separator), ".xml"
	glob := pref + "*" + suff
	files, _ := filepath.Glob(glob)
	if len(files) == 0 {
		return fmt.Errorf("no quota XML files found in %s", quotaDir)
	}
	newHosts := make(map[string]map[string]*config.Host)
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
		quota := make(map[string]*config.Host)
		for _, b := range browsers.Browsers {
			for _, v := range b.Versions {
				for _, r := range v.Regions {
					for _, h := range r.Hosts {
						quota[h.Sum()] = &h
					}
				}
			}
		}
		user := strings.TrimPrefix(strings.TrimSuffix(fn, suff), pref)
		newHosts[user] = quota
		log.Printf("[INIT] [Loaded quota for user: %s]", user)
	}
	lock.Lock()
	defer lock.Unlock()
	hosts = newHosts
	return nil
}

func init() {
	flag.BoolVar(&guestAccessAllowed, "guests-allowed", false, "Allow guest (unauthenticated) users to access the grid")
	flag.StringVar(&guestUserName, "guests-quota", "guest", "Which quota file to use for guests")
	flag.StringVar(&listen, "listen", ":8888", "host and port to listen to")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "request timeout")
	flag.DurationVar(&responseTime, "response-time", 2*time.Second, "response time limit")
	flag.IntVar(&limit, "limit", 10, "simultaneous http requests")
	flag.StringVar(&quotaDir, "quota-dir", "quota", "quota directory")
	flag.DurationVar(&gracePeriod, "grace-period", 300*time.Second, "graceful shutdown period")
	flag.BoolVar(&version, "version", false, "Show version and exit")
	flag.Parse()

	if version {
		showVersion()
		os.Exit(0)
	}

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
	stop := make(chan os.Signal)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("[INIT] [Listen on %s]", listen)
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
