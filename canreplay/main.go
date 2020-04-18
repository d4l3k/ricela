package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/d4l3k/ricela/can"
	"github.com/guptarohit/asciigraph"
)

var (
	graph    = flag.Bool("graph", false, "whether to graph the series")
	filter   = flag.String("filter", "", "regexp filter for the keys")
	hidezero = flag.Bool("hidezero", false, "hide all zero values")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		log.Fatalf("%+v", err)
	}
}

func readAllRecords(r io.Reader) ([]can.Record, error) {
	var records []can.Record
	s := bufio.NewScanner(r)
	for s.Scan() {
		var record can.Record
		if err := json.Unmarshal(s.Bytes(), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func run() error {
	records, err := readAllRecords(os.Stdin)
	if err != nil {
		return err
	}

	const graphWidth = 80

	start := records[0].Time
	end := records[len(records)-1].Time
	step := end.Sub(start) / graphWidth

	type Point struct {
		X time.Duration
		Y float64
	}

	series := map[string][]Point{}

	for _, record := range records {
		for key, value := range can.FrameToKV(record.Frame) {
			match, err := regexp.MatchString(*filter, key)
			if err != nil {
				return err
			}
			if !match {
				continue
			}

			if *graph {
				series[key] = append(series[key], Point{X: record.Time.Sub(start), Y: value})
			} else {
				if *hidezero && value == 0 {
					continue
				}
				fmt.Printf("%s: %s = %f\n", record.Time, key, value)
			}
		}
	}

	for key, values := range series {
		data := make([]float64, graphWidth)
		bucketVals := map[int]float64{}
		for _, value := range values {
			bucket := int(value.X / step)
			bucketVals[bucket] = value.Y
		}

		value := values[0].Y
		for i := range data {
			newVal, ok := bucketVals[i]
			if ok {
				value = newVal
			}
			data[i] = value
		}

		fmt.Printf("# %s:\n", key)
		if allSame(data) {
			fmt.Println("all values the same:", data[0])
		} else {
			fmt.Println(asciigraph.Plot(
				data,
				asciigraph.Height(10),
				asciigraph.Caption(fmt.Sprintf("%s - %s", start, end)),
			))
		}
		fmt.Println()
	}
	return nil
}

func allSame(values []float64) bool {
	if len(values) <= 1 {
		return true
	}
	first := values[0]
	for _, v := range values[1:] {
		if v != first {
			return false
		}
	}
	return true
}
