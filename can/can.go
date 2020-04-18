package can

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"time"
)

type Frame struct {
	ID        int // 29 bit if ide set, 11 bit otherwise
	Data      [8]byte
	Timestamp int
}

func (cf Frame) Uint64() uint64 {
	r := bytes.NewReader(cf.Data[:])
	var v uint64
	if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
		panic(err)
	}
	return v
}

func (cf Frame) ReadBits(n, start int) int64 {
	b := cf.Uint64()
	return int64((b << (64 - n - start)) >> (64 - n))
}

func (cf Frame) ReadFloat(n, start int, offset, scale float64) float64 {
	if scale == 0 {
		panic(fmt.Sprintf("%X: scale must not be 0", cf.ID))
	}
	v := cf.ReadBits(n, start)
	return (float64(v))*scale + offset
}

func (cf Frame) String() string {
	return fmt.Sprintf("Frame(id=%X, data=%+v, timestamp=%d)", cf.ID, cf.Data, cf.Timestamp)
}

func ParseCSV(row []string) (Frame, error) {
	var frame Frame
	id, err := strconv.ParseUint(row[0], 10, 64)
	if err != nil {
		return frame, err
	}
	frame.ID = int(id)
	for i, b := range row[1:9] {
		parsed, err := strconv.ParseUint(b, 10, 64)
		if err != nil {
			return frame, err
		}
		frame.Data[i] = byte(parsed)
	}
	timestamp, err := strconv.ParseUint(row[5], 10, 64)
	if err != nil {
		return frame, err
	}
	frame.Timestamp = int(timestamp)
	return frame, nil
}

// Record for recording canbus events for later analysis.
type Record struct {
	Frame Frame
	Time  time.Time
}

func FrameToKV(frame Frame) map[string]float64 {
	kv := map[string]float64{}
	set := func(key string, value float64) {
		kv[key] = value
	}

	switch frame.ID {
	case 0x108:
		set("rear_torque_request_nm", frame.ReadFloat(13, 12, 0, 0.22222))
		set("rear_torque_actual_nm", frame.ReadFloat(13, 27, 0, 0.22222))
		set("rear_axel_rpm", frame.ReadFloat(16, 40, 0, 0.1))
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
	case 0x186:
		set("front_torque_request_nm", frame.ReadFloat(13, 12, 0, 0.22222))
		set("front_torque_actual_nm", frame.ReadFloat(13, 27, 0, 0.22222))
		set("front_axel_rpm", frame.ReadFloat(16, 40, 0, 0.1))
	case 0x1D5:
		set("front_torque2_request_nm", frame.ReadFloat(15, 8, 0, 0.1))
		set("front_torque2_nm", frame.ReadFloat(13, 24, 0, 0.25))
	case 0x1D8:
		set("rear_torque2_request_nm", frame.ReadFloat(15, 8, 0, 0.1))
		set("rear_torque2_nm", frame.ReadFloat(13, 24, 0, 0.25))
	case 0x212:
		set("bms_contactors", frame.ReadFloat(3, 8, 0, 1))
		set("bms_state", frame.ReadFloat(4, 11, 0, 1))
		set("isolation_restance_kohm", frame.ReadFloat(10, 19, 0, 1))
		set("bms_charge_status", frame.ReadFloat(3, 32, 0, 1))
		set("bms_charge_power_available_kw", frame.ReadFloat(11, 38, 0, 0.125))
		set("min_batt_temp_c", frame.ReadFloat(8, 56, -40, 0.5))
	case 0x229:
		set("gear_lever_position", frame.ReadFloat(3, 12, 0, 1))
		set("gear_lever_button", frame.ReadFloat(2, 16, 0, 1))
	case 0x241:
		set("battery_coolant_flow_rate_lpm", frame.ReadFloat(9, 0, 0, 0.1))
		set("powertrain_coolant_flow_rate", frame.ReadFloat(9, 22, 0, 0.1))
	case 0x249:
		set("left_stalk_horizontal", frame.ReadFloat(2, 12, 0, 1))
		set("left_stalk_button", frame.ReadFloat(2, 14, 0, 1))
		set("left_stalk_vertical", frame.ReadFloat(3, 16, 0, 1))
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
	case 0x266:
		set("rear_power_kw", frame.ReadFloat(11, 0, 0, 0.5))
		set("rear_heat_power_optimal_kw", frame.ReadFloat(8, 32, 0, 0.08))
		set("rear_heat_power_max_kw", frame.ReadFloat(8, 40, 0, 0.08))
		set("rear_heat_power_kw", frame.ReadFloat(8, 48, 0, 0.08))
	case 0x292:
		set("ui_state_of_charge_pct", frame.ReadFloat(10, 0, 0, 0.1))
		set("min_state_of_charge_pct", frame.ReadFloat(10, 10, 0, 0.1))
		set("max_state_of_charge_pct", frame.ReadFloat(10, 20, 0, 0.1))
		set("average_state_of_charge_pct", frame.ReadFloat(10, 30, 0, 0.1))
	case 0x2E5:
		set("front_power_kw", frame.ReadFloat(11, 0, 0, 0.5))
		set("front_heat_power_optimal_kw", frame.ReadFloat(8, 32, 0, 0.08))
		set("front_heat_power_max_kw", frame.ReadFloat(8, 40, 0, 0.08))
		set("front_heat_power_kw", frame.ReadFloat(8, 48, 0, 0.08))
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
	return kv
}
