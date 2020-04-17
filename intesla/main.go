package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

var (
	canAddr = flag.String("canaddr", "http://192.168.123.151", "address of the canbus device")
	bind    = flag.String("bind", ":2112", "address to bind the http server to")
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("%+v", err)
	}
}

type CanFrame struct {
	ID        int // 29 bit if ide set, 11 bit otherwise
	Data      [8]byte
	Timestamp int
}

func (cf CanFrame) Uint64() uint64 {
	r := bytes.NewReader(cf.Data[:])
	var v uint64
	if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
		panic(err)
	}
	return v
}

func (cf CanFrame) ReadBits(n, start int) int64 {
	b := cf.Uint64()
	return int64((b << (64 - n - start)) >> (64 - n))
}

func (cf CanFrame) ReadFloat(n, start int, offset, scale float64) float64 {
	if scale == 0 {
		panic(fmt.Sprintf("%X: scale must not be 0", cf.ID))
	}
	v := cf.ReadBits(n, start)
	return (float64(v) + offset) * scale
}

func (cf CanFrame) String() string {
	return fmt.Sprintf("CanFrame(id=%X, data=%+v, timestamp=%d)", cf.ID, cf.Data, cf.Timestamp)
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
		return processCan(ctx)
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

		var frame CanFrame
		id, err := strconv.ParseUint(row[0], 10, 64)
		if err != nil {
			return err
		}
		frame.ID = int(id)
		for i, b := range row[1:9] {
			parsed, err := strconv.ParseUint(b, 10, 64)
			if err != nil {
				return err
			}
			frame.Data[i] = byte(parsed)
		}
		timestamp, err := strconv.ParseUint(row[5], 10, 64)
		if err != nil {
			return err
		}
		frame.Timestamp = int(timestamp)

		switch frame.ID {
		case 0x108:
			set("torque_request_nm", frame.ReadFloat(13, 12, 0, 0.22222))
			set("torque_actual_nm", frame.ReadFloat(13, 27, 0, 0.22222))
			set("axel_rpm", frame.ReadFloat(16, 40, 0, 0.1))
		case 0x118:
			set("drive_state", frame.ReadFloat(3, 16, 0, 1))
			set("brake_pedal", frame.ReadFloat(2, 19, 0, 1))
			set("gear", frame.ReadFloat(3, 21, 0, 1))
			set("brake_hold", frame.ReadFloat(1, 26, 0, 1))
			set("immobilizer", frame.ReadFloat(3, 27, 0, 1))
			set("pedal_position_pct", frame.ReadFloat(8, 32, 0, 0.4))
			set("traction_control", frame.ReadFloat(3, 40, 0, 1))
			set("parking_brake", frame.ReadFloat(2, 44, 0, 1))
			set("track_mode", frame.ReadFloat(2, 48, 0, 1))
		case 0x129:
			set("steering_angle_deg", frame.ReadFloat(14, 16, -819.2, 0.1))
			set("steering_speed_dps", frame.ReadFloat(14, 32, -4096, 0.5))
		case 0x132:
			set("battery_voltage", frame.ReadFloat(16, 0, 0, 0.01))
			set("battery_current", frame.ReadFloat(16, 16, 1000, -0.01))
			set("raw_battery_current", frame.ReadFloat(16, 32, 1000, -0.05))
			set("charge_time_remaining", frame.ReadFloat(12, 48, 0, 1))
		case 0x154:
			set("rear_raw_torque_nm", frame.ReadFloat(12, 40, 0, 0.25))
		case 0x1D4:
			set("front_raw_torque_nm", frame.ReadFloat(12, 40, 0, 0.25))
		case 0x241:
			set("battery_coolant_flow_rate_lpm", frame.ReadFloat(9, 0, 0, 0.1))
			set("powertrain_coolant_flow_rate", frame.ReadFloat(9, 22, 0, 0.1))
		case 0x252:
			set("regen_power_limit_kw", frame.ReadFloat(16, 0, 0, 0.01))
			set("discharge_power_limit_kw", frame.ReadFloat(16, 16, 0, 0.01))
			set("max_heat_parked_kw", frame.ReadFloat(10, 32, 0, 0.1))
			set("hvac_max_power_kw", frame.ReadFloat(10, 50, 0, 0.02))
		case 0x257:
			set("signed_speed", frame.ReadFloat(12, 12, -25, 0.05))
			set("ui_speed", frame.ReadFloat(8, 24, 0, 1))
			set("mph_kph_flag", frame.ReadFloat(1, 32, 0, 1))
		case 0x261:
			set("12v_battery_voltage", frame.ReadFloat(12, 0, 0, 0.005444))
			set("12v_battery_temp_c", frame.ReadFloat(16, 16, 0, 0.01))
			set("12v_battery_amp_hours", frame.ReadFloat(14, 32, 0, 0.01))
			set("12v_battery_current_amp", frame.ReadFloat(16, 48, 0, 0.005))
		case 0x264:
			set("charge_line_voltage", frame.ReadFloat(14, 0, 0, 0.0333))
			set("charge_line_current_amp", frame.ReadFloat(9, 14, 0, 0.1))
			set("charge_line_power_kw", frame.ReadFloat(8, 24, 0, 0.1))
			set("charge_line_current_limit_amp", frame.ReadFloat(10, 32, 0, 0.1))
		case 0x292:
			set("ui_state_of_charge_pct", frame.ReadFloat(10, 0, 0, 0.1))
			set("min_state_of_charge_pct", frame.ReadFloat(10, 10, 0, 0.1))
			set("max_state_of_charge_pct", frame.ReadFloat(10, 20, 0, 0.1))
			set("average_state_of_charge_pct", frame.ReadFloat(10, 30, 0, 0.1))
		case 0x293:
			set("ui_steering_mode", frame.ReadFloat(2, 0, 0, 1))
			set("ui_traction_control_mode", frame.ReadFloat(3, 2, 0, 1))
		case 0x321:
			set("coolant_temp_battery_inlet_c", frame.ReadFloat(10, 0, -40, 0.125))
			set("coolant_temp_powertrain_inlet_c", frame.ReadFloat(10, 10, -40, 0.125))
			set("ambient_temp_raw_c", frame.ReadFloat(8, 24, -40, 0.5))
			set("ambient_temp_filtered_c", frame.ReadFloat(8, 40, -40, 0.5))
		case 0x333:
			set("ui_charge_current_limit_amp", frame.ReadFloat(7, 8, 0, 1))
			set("ui_charge_limit_pct", frame.ReadFloat(10, 16, 0, 0.1))
		case 0x336:
			set("power_rating_kw", frame.ReadFloat(9, 0, 0, 1))
			set("regen_rating_kw", frame.ReadFloat(8, 16, -100, 1))
		case 0x352:
			set("full_battery_capacity_kwh", frame.ReadFloat(10, 0, 0, 0.1))
			set("remaining_battery_chage_kwh", frame.ReadFloat(10, 10, 0, 0.1))
			set("expected_remaining_kwh", frame.ReadFloat(10, 20, 0, 0.1))
			set("ideal_remaining_kwh", frame.ReadFloat(10, 30, 0, 0.1))
			set("kwh_to_complete_charge", frame.ReadFloat(10, 40, 0, 0.1))
			set("energy_buffer_kwh", frame.ReadFloat(10, 50, 0, 0.1))
		case 0x376:
			set("inverter_pcb_temp_c", frame.ReadFloat(8, 0, -40, 1))
			set("inverter_temp_c", frame.ReadFloat(8, 8, -40, 1))
			set("stator_temp_c", frame.ReadFloat(8, 16, -40, 1))
			set("inverter_capbank_temp_c", frame.ReadFloat(8, 24, -40, 1))
			set("inverter_heatsink_temp_c", frame.ReadFloat(8, 32, -40, 1))
			set("inverter_temp_pct", frame.ReadFloat(8, 40, 0, 0.4))
			set("stator_temp_pct", frame.ReadFloat(8, 48, 0, 0.4))
		case 0x396:
			set("rear_oil_pump_state", frame.ReadFloat(3, 0, 0, 1))
			set("rear_oil_flow_target_lpm", frame.ReadFloat(8, 8, 0, 0.06))
			set("rear_oil_flow_actual_lpm", frame.ReadFloat(8, 16, 0, 0.06))
		case 0x3B6:
			set("odometer_meter", frame.ReadFloat(32, 0, 0, 1))
		case 0x3D2:
			set("total_discharge_kwh", frame.ReadFloat(32, 0, 0, 0.001))
			set("total_charge_kwh", frame.ReadFloat(32, 32, 0, 0.001))
		case 0x3D8:
			set("elevation_meter", frame.ReadFloat(16, 0, 0, 1))
		case 0x3FE:
			set("front_left_brake_temp", frame.ReadFloat(10, 0, -40, 1))
			set("front_right_brake_temp", frame.ReadFloat(10, 10, -40, 1))
			set("rear_left_brake_temp", frame.ReadFloat(10, 20, -40, 1))
			set("rear_right_brake_temp", frame.ReadFloat(10, 30, -40, 1))
		case 0x541:
			set("fast_charge_max_power_limit_kw", frame.ReadFloat(13, 0, 0, 0.062256))
			set("fast_charge_max_current_limit_amp", frame.ReadFloat(16, 16, 0, 0.073242))
		}
	}
}
