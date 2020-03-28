package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/jsgoecke/tesla"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

var (
	bind     = flag.String("bind", ":2112", "address to bind to")
	pollTime = flag.Duration("pollTime", 1*time.Minute, "polling frequency")
)

func main() {
	log.SetFlags(log.Flags() | log.Lshortfile)
	flag.Parse()
	var r RiceLa
	if err := r.run(); err != nil {
		log.Fatalf("%+v", err)
	}
}

func (r *RiceLa) pollVehicleData(v *tesla.Vehicle) error {
	log.Printf("Polling %s: %v", v.DisplayName, v.ID)
	req, err := http.NewRequest("GET", tesla.BaseURL+"/vehicles/"+strconv.FormatInt(v.ID, 10)+"/vehicle_data", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.client.Token.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	res, err := r.client.HTTP.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return errors.New(res.Status)
	}
	defer res.Body.Close()

	out := map[string]interface{}{}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return err
	}
	spew.Dump(out)

	r.processCounter("tesla", out["response"])

	return nil
}

func (r *RiceLa) processCounter(key string, v interface{}) {
	switch v := v.(type) {
	case map[string]interface{}:
		for k, v := range v {
			key := key + ":" + k
			r.processCounter(key, v)
		}
	case float64:
		r.setCounter(key, v)
	case int:
		r.setCounter(key, float64(v))
	case int64:
		r.setCounter(key, float64(v))
	case int32:
		r.setCounter(key, float64(v))
	case float32:
		r.setCounter(key, float64(v))
	case bool:
		if v {
			r.setCounter(key, 1)
		} else {
			r.setCounter(key, 0)
		}
	default:
		if v == nil {
			r.setCounter(key, 0)
		}
	}
}

func (r *RiceLa) setCounter(key string, v float64) {
	log.Println(key, v)

	r.mu.Lock()
	defer r.mu.Unlock()

	g, ok := r.mu.gauges[key]
	if !ok {
		g = promauto.NewGauge(prometheus.GaugeOpts{Name: key})
		r.mu.gauges[key] = g
	}
	g.Set(v)
}

type RiceLa struct {
	client      *tesla.Client
	chargepoint *chargepoint.Client

	mu struct {
		sync.Mutex

		gauges map[string]prometheus.Gauge
	}
}

func (r *RiceLa) run() error {
	r.mu.gauges = map[string]prometheus.Gauge{}

	eg, ctx := errgroup.WithContext(context.Background())

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	eg.Go(func() error {
		var err error
		r.client, err = tesla.NewClient(
			&tesla.Auth{
				ClientID:     os.Getenv("TESLA_CLIENT_ID"),
				ClientSecret: os.Getenv("TESLA_CLIENT_SECRET"),
				Email:        os.Getenv("TESLA_USERNAME"),
				Password:     os.Getenv("TESLA_PASSWORD"),
			})
		if err != nil {
			return errors.Wrapf(err, "failed to create client")
		}

		vehicles, err := r.client.Vehicles()
		if err != nil {
			return errors.Wrapf(err, "failed to get vehicles")
		}
		for _, v := range vehicles {
			v := v
			eg.Go(func() error {
				t := time.NewTicker(*pollTime)
				for {
					if err := r.pollVehicleData(v.Vehicle); err != nil {
						return err
					}

					select {
					case <-ctx.Done():
						return nil
					case <-t.C:
					}
				}
			})
		}
		return nil
	})

	s := http.Server{
		Addr:    *bind,
		Handler: mux,
	}

	eg.Go(func() error {
		fmt.Println("Listening...", s.Addr)
		return s.ListenAndServe()
	})

	eg.Go(func() error {
		<-ctx.Done()

		ctxShutDown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer func() {
			cancel()
		}()

		if err := s.Shutdown(ctxShutDown); err != nil {
			log.Fatal(err)
		}
		return nil
	})

	return eg.Wait()
}
