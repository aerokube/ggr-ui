package main

import (
	"context"
	"crypto/md5"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/aandryashin/ggr-ui/config"
)

var (
	hosts   = make(map[string]string)
	limitCh chan struct{}
)

var (
	listen      string
	limit       int
	quotaDir    string
	gracePeriod time.Duration
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
			log.Printf("error reading configuration file %s: %v", fn, err)
			continue
		}
		var browsers config.Browsers
		if err := xml.Unmarshal(file, &browsers); err != nil {
			log.Printf("error parsing configuration file %s: %v", fn, err)
			continue
		}
		for _, b := range browsers.Browsers {
			for _, v := range b.Versions {
				for _, r := range v.Regions {
					for _, h := range r.Hosts {
						url := fmt.Sprintf("http://%s", net.JoinHostPort(h.Name, strconv.Itoa(h.Port)))
						hosts[fmt.Sprintf("%x", md5.Sum([]byte(url)))] = url
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
	flag.Parse()
	limitCh = make(chan struct{}, limit)
	err := configure()
	if err != nil {
		log.Fatalf("loading quota files: %v\n", err)
	}

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
		log.Fatalf("starting http server: %v\n", err)
	case <-stop:
	}

	log.Printf("starting shutdown in %v\n", gracePeriod)
	ctx, cancel := context.WithTimeout(context.Background(), gracePeriod)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("shuting down: %v\n", err)
	}
}
