package sysmetrics

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/ssimunic/gosensors"
)

var numberRegexp = regexp.MustCompile(`-?\d*\.?\d*`)

var invalidCharsRegexp = regexp.MustCompile(`[^a-zA-Z0-9_:]+`)

func NormalizeKey(s string) string {
	return invalidCharsRegexp.ReplaceAllLiteralString(s, "_")
}

var counters = map[string]prometheus.Gauge{}

func getCounter(key string) prometheus.Gauge {
	key = "sysmetrics:" + key
	counter, ok := counters[key]
	if !ok {
		counter = promauto.NewGauge(prometheus.GaugeOpts{
			Name: key,
		})
		counters[key] = counter
	}
	return counter
}

func monitorLMSensors() error {
	sensors, err := gosensors.NewFromSystem()
	if err != nil {
		return err
	}

	for chip, entries := range sensors.Chips {
		for sensorType, value := range entries {
			numberStr := numberRegexp.FindString(value)
			if len(numberStr) == 0 {
				continue
			}

			key := NormalizeKey(chip + ":" + sensorType)
			parsed, err := strconv.ParseFloat(numberStr, 64)
			if err != nil {
				return err
			}
			counter := getCounter(key)
			counter.Set(parsed)
		}
	}
	return nil
}

func monitorArmTemp() error {
	f, err := os.Open("/sys/class/thermal/thermal_zone0/temp")
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	defer f.Close()

	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}

	n, err := strconv.Atoi(strings.TrimSpace(string(bytes)))
	if err != nil {
		return err
	}

	temp := float64(n) / 1000
	getCounter("cpu_temp_c").Set(temp)
	return nil
}

var dbRegexp = regexp.MustCompile(`(\w+): '(-?\d+.?\d+) (dBm?)`)

func parseNASOutput(s string) map[string]float64 {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	out := map[string]float64{}
	category := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			continue
		}
		if strings.HasSuffix(line, ":") {
			category = line[:len(line)-1]
		}
		matches := dbRegexp.FindStringSubmatch(line)
		if len(matches) == 0 {
			continue
		}
		value, err := strconv.ParseFloat(matches[2], 64)
		if err != nil {
			continue
		}
		key := category + ":" + matches[1] + "_" + matches[3]
		out[key] = value
	}
	return out
}

func fileExists(filename string) bool {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return false
	}
	return true
}

func monitorNAS(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	const device = "/dev/cdc-wdm0"
	if !fileExists(device) {
		return nil
	}
	out, err := exec.CommandContext(
		ctx, "qmicli", "-d", device, "--nas-get-signal-info", "--client-cid=19", "--client-no-release-cid",
	).Output()
	if err != nil {
		return err
	}
	for key, value := range parseNASOutput(string(out)) {
		getCounter(key).Set(value)
	}
	return nil
}

func Monitor(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := monitorLMSensors(); err != nil {
			log.Printf("error monitoring sensors: %+v", err)
		}
		if err := monitorArmTemp(); err != nil {
			log.Printf("error monitoring arm temperature: %+v", err)
		}
		if err := monitorNAS(ctx); err != nil {
			log.Printf("error monitoring NAS info: %+v", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
