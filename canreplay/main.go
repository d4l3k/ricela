package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"regexp"
	"time"

	"github.com/d4l3k/ricela/can"
	"github.com/guptarohit/asciigraph"
)

var (
	graph       = flag.Bool("graph", false, "whether to graph the series")
	filter      = flag.String("filter", "", "regexp filter for the keys")
	hidezero    = flag.Bool("hidezero", false, "hide all zero values")
	zerotosixty = flag.Bool("zerotosixty", false, "estimate 0-60 times")
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

	var speeds []Point

	series := map[string][]Point{}

	for _, record := range records {
		dur := record.Time.Sub(start)
		for key, value := range can.FrameToKV(record.Frame) {
			point := Point{X: dur, Y: value}
			if key == can.SignedSpeedKey {
				speeds = append(speeds, point)
			}

			match, err := regexp.MatchString(*filter, key)
			if err != nil {
				return err
			}
			if !match {
				continue
			}

			if *graph {
				series[key] = append(series[key], point)
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
		min := math.MaxFloat64
		max := -math.MaxFloat64
		for _, value := range values {
			bucket := int(value.X / step)
			bucketVals[bucket] = value.Y

			if value.Y > max {
				max = value.Y
			}
			if value.Y < min {
				min = value.Y
			}
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
		fmt.Printf("max = %f, min = %f\n", max, min)
		fmt.Println()
	}

	if *zerotosixty {
		var low Point
		for _, speed := range speeds {
			if speed.Y < 5 {
				low = speed
			}
			if speed.Y > 60 && low.X != 0 {
				timepermph := float64(speed.X-low.X) / (speed.Y - low.Y)
				fmt.Println("start", low)
				fmt.Println("end", speed)

				adjustedLow := low.X - time.Duration(timepermph*low.Y)
				adjustedHigh := speed.X + time.Duration(timepermph*(60-speed.Y))
				fmt.Println("estimated 0-60:", adjustedHigh-adjustedLow)

				low = Point{}
			}
		}
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
