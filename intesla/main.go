package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/alecthomas/units"
	"github.com/d4l3k/ricela/can"
	"github.com/d4l3k/ricela/sysmetrics"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

var (
	canAddr        = flag.String("canaddr", "http://192.168.123.10", "address of the canbus device")
	bind           = flag.String("bind", ":2112", "address to bind the http server to")
	metricPollTime = flag.Duration("metricPollTime", 15*time.Second, "time to poll system metrics")
	logFile        = flag.String("logfile", "log.json", "file to launch data to")
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
	log.SetFlags(log.Flags() | log.Lshortfile)

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
		return errors.Wrap(sysmetrics.Monitor(ctx, *metricPollTime), "sysmetrics")
	})

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.Handler())

	s := http.Server{
		Addr:    *bind,
		Handler: mux,
	}

	eg.Go(func() error {
		fmt.Println("Listening...", s.Addr)
		return errors.Wrap(s.ListenAndServe(), "ListenAndServe")
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

	log.Printf("logging to %s", *logFile)
	f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	defer f.Close()
	logging := false
	var logged units.MetricBytes

	write := func(buf []byte) error {
		n, err := f.Write(buf)
		if err != nil {
			return err
		}
		logged += units.MetricBytes(n)
		return nil
	}

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

			if key == can.GearKey {
				// Log if it's in drive or reverse.
				logging = value == can.GearDrive || value == can.GearReverse
			}
		}

		if logging {
			record := can.Record{
				Frame: frame,
				Time:  time.Now(),
			}
			body, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if err := write(body); err != nil {
				return err
			}
			if err := write([]byte("\n")); err != nil {
				return err
			}
		}

		// If we've logged more than 1GB truncate the file.
		if logged >= 1*units.GB {
			if _, err := f.Seek(0, 0); err != nil {
				return err
			}
			if err := f.Truncate(0); err != nil {
				return err
			}
			logged = 0
		}
	}
}
