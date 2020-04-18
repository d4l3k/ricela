package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/d4l3k/ricela/can"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("%+v", err)
	}
}

func run() error {
	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		var record can.Record
		if err := json.Unmarshal(s.Bytes(), &record); err != nil {
			return err
		}

		for key, value := range can.FrameToKV(record.Frame) {
			fmt.Printf("%s: %s = %f\n", record.Time, key, value)
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	return nil
}
