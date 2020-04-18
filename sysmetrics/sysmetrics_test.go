package sysmetrics

import (
	"reflect"
	"testing"
)

func TestParseNASOutput(t *testing.T) {
	const in = `
	[/dev/cdc-wdm0] Successfully got signal info
	LTE:
		RSSI: '-64 dBm'
		RSRQ: '-12 dB'
		RSRP: '-97 dBm'
		SNR: '17.4 dB'
	`
	out := parseNASOutput(in)
	want := map[string]float64{
		"LTE:RSSI_dBm": -64,
		"LTE:RSRQ_dB":  -12,
		"LTE:RSRP_dBm": -97,
		"LTE:SNR_dB":   17.4,
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("parseNASOutput(%q) = %+v; expected %+v", in, out, want)
	}
}
