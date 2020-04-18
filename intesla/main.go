package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/d4l3k/ricela/can"
	"github.com/d4l3k/ricela/sysmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

var (
	canAddr        = flag.String("canaddr", "http://192.168.123.10", "address of the canbus device")
	bind           = flag.String("bind", ":2112", "address to bind the http server to")
	metricPollTime = flag.Duration("metricPollTime", 15*time.Second, "time to poll system metrics")
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("%+v", err)
	}
}

var counters = map[string]prometheus.Gauge{}

func set(name string, val float64) {
	counter, ok := counters[name]
	if !ok {
		counter = promauto.NewGauge(prometheus.GaugeOpts{
			Name: "canbus:" + name,
		})
		counters[name] = counter
	}
	counter.Set(val)
}

func run() error {
	flag.Parse()

	eg, ctx := errgroup.WithContext(context.Background())

	eg.Go(func() error {
		for {
			if err := processCan(ctx); err != nil {
				log.Printf("failed to process can: %+v", err)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.NewTimer(10 * time.Second).C:
			}
		}
	})

	eg.Go(func() error {
		return sysmetrics.Monitor(ctx, *metricPollTime)
	})

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.Handler())

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

func processCan(ctx context.Context) error {
	log.Printf("streaming from %q", *canAddr)
	req, err := http.NewRequestWithContext(ctx, "GET", *canAddr, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	reader := csv.NewReader(resp.Body)

	for {
		row, err := reader.Read()
		if err != nil {
			return err
		}

		frame, err := can.ParseCSV(row)
		if err != nil {
			return err
		}

		for key, value := range can.FrameToKV(frame) {
			set(key, value)
		}
	}
}
