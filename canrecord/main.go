package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/d4l3k/ricela/can"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("%+v", err)
	}
}

func run() error {
	resp, err := http.Get("http://192.168.123.10")
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

		record := can.Record{
			Frame: frame,
			Time:  time.Now(),
		}

		bytes, err := json.Marshal(record)
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", bytes)
	}
}
