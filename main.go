package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/d4l3k/ricela/chargepoint"
	"github.com/d4l3k/ricela/sysmetrics"
	"github.com/golang/geo/s2"

	"github.com/davecgh/go-spew/spew"
	"github.com/jsgoecke/tesla"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

var (
	bind                = flag.String("bind", ":2112", "address to bind to")
	standbyPollTime     = flag.Duration("standbyPollTime", 1*time.Minute, "polling frequency")
	drivePollTime       = flag.Duration("drivePollTime", 15*time.Second, "polling frequency")
	activePollTime      = flag.Duration("activePollTime", 5*time.Second, "polling frequency")
	chargePointPollTime = flag.Duration("chargePointPollTime", 5*time.Minute, "polling frequency")
	carServerAddr       = flag.String("carserver", "http://localhost:27654/diag_vitals", "car server vitals endpoint")
)

const (
	StateCharging = "Charging"
	StateComplete = "Complete"
)

type Charger interface {
	DistanceInMeters(a s2.LatLng) float64
	Start(ctx context.Context, r *RiceLa) error
}

type ChargePointCharger struct {
	DeviceID int64
	LatLng   s2.LatLng
}

func (c ChargePointCharger) DistanceInMeters(a s2.LatLng) float64 {
	const earthRadius = 6_371_000
	angle := c.LatLng.Distance(a)
	return earthRadius * angle.Radians()
}

func (c ChargePointCharger) Start(ctx context.Context, r *RiceLa) error {
	_, err := r.chargepoint.StartSession(ctx, c.DeviceID)
	return err
}

var knownChargers = []Charger{
	ChargePointCharger{DeviceID: 1947511, LatLng: s2.LatLngFromDegrees(47.630007, -122.133969)},
}

func main() {
	log.SetFlags(log.Flags() | log.Lshortfile)
	flag.Parse()
	var r RiceLa
	if err := r.run(); err != nil {
		log.Fatalf("%+v", err)
	}
}

type ClimateState struct {
	InsideTemp              float64     `json:"inside_temp"`
	OutsideTemp             float64     `json:"outside_temp"`
	DriverTempSetting       float64     `json:"driver_temp_setting"`
	PassengerTempSetting    float64     `json:"passenger_temp_setting"`
	LeftTempDirection       float64     `json:"left_temp_direction"`
	RightTempDirection      float64     `json:"right_temp_direction"`
	IsAutoConditioningOn    bool        `json:"is_auto_conditioning_on"`
	IsFrontDefrosterOn      interface{} `json:"is_front_defroster_on"`
	IsRearDefrosterOn       bool        `json:"is_rear_defroster_on"`
	FanStatus               interface{} `json:"fan_status"`
	IsClimateOn             bool        `json:"is_climate_on"`
	MinAvailTemp            float64     `json:"min_avail_temp"`
	MaxAvailTemp            float64     `json:"max_avail_temp"`
	SeatHeaterLeft          int         `json:"seat_heater_left"`
	SeatHeaterRight         int         `json:"seat_heater_right"`
	SeatHeaterRearLeft      int         `json:"seat_heater_rear_left"`
	SeatHeaterRearRight     int         `json:"seat_heater_rear_right"`
	SeatHeaterRearCenter    int         `json:"seat_heater_rear_center"`
	SeatHeaterRearRightBack int         `json:"seat_heater_rear_right_back"`
	SeatHeaterRearLeftBack  int         `json:"seat_heater_rear_left_back"`
	SmartPreconditioning    bool        `json:"smart_preconditioning"`
}

type VehicleData struct {
	UserID    int64  `json:"user_id"`
	VehicleID int64  `json:"vehicle_id"`
	VIN       string `json:"vin"`
	State     string `json:"online"`

	ChargeState  tesla.ChargeState  `json:"charge_state"`
	VehicleState tesla.VehicleState `json:"vehicle_state"`
	ClimateState ClimateState       `json:"climate_state"`
	DriveState   tesla.DriveState   `json:"drive_state"`
}

type VehicleDataResponse struct {
	Response VehicleData `json:"response"`
}

func (r *RiceLa) getVehicleData(ctx context.Context, v *tesla.Vehicle) (*VehicleData, error) {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	log.Printf("Polling %s: %v", v.DisplayName, v.ID)
	req, err := http.NewRequestWithContext(ctx, "GET", tesla.BaseURL+"/vehicles/"+strconv.FormatInt(v.ID, 10)+"/vehicle_data", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.client.Token.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	res, err := r.client.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != 200 {
		return nil, errors.Errorf("%s: %s", res.Status, body)
	}

	out := map[string]interface{}{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	spew.Dump(out)

	count := r.processCounter("tesla", out["response"])
	log.Printf("updated %d counters", count)

	var resp VehicleDataResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, errors.Wrapf(err, "unmarshalling vehicle_data")
	}

	return &resp.Response, nil
}

var counterStrs = map[string]float64{
	"--":         -1,
	"NONE":       -1,
	"PRESENT":    1,
	"ENGAGED":    1,
	"DISENGAGED": 0,
	"LATCHED":    1,
	"NOMINAL":    1,
	"FAULT":      0,
	"ERROR":      0,
	"DRIVE":      2,
	"PARKED":     1,
	"REVERSE":    3,
	"NEUTRAL":    4,
	"On":         1,
	"Off":        0,
	"Stopped":    0,
	"IDLE":       0,
	"ACTIVE":     1,
	"on":         1,
	"off":        0,
	"yes":        1,
	"no":         0,
	"":           -1,
}

func (r *RiceLa) processCounter(key string, v interface{}) int {
	switch v := v.(type) {
	case map[string]interface{}:
		count := 0
		for k, v := range v {
			key := key + ":" + k
			count += r.processCounter(key, v)
		}
		return count
	case float64:
		r.setCounter(key, v)
		return 1
	case int:
		r.setCounter(key, float64(v))
		return 1
	case int64:
		r.setCounter(key, float64(v))
		return 1
	case int32:
		r.setCounter(key, float64(v))
		return 1
	case float32:
		r.setCounter(key, float64(v))
		return 1
	case bool:
		if v {
			r.setCounter(key, 1)
		} else {
			r.setCounter(key, 0)
		}
		return 1
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			r.setCounter(key, f)
			return 1
		}

		f, ok := counterStrs[v]
		if ok {
			r.setCounter(key, f)
			return 1
		}
		return 0
	default:
		if v == nil {
			r.setCounter(key, 0)
			return 1
		}
		return 0
	}
}

func (r *RiceLa) setCounter(key string, v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	g, ok := r.mu.gauges[key]
	if !ok {
		g = promauto.NewGauge(prometheus.GaugeOpts{Name: key})
		r.mu.gauges[key] = g
	}
	g.Set(v)
}

type RiceLa struct {
	client      *tesla.Client
	chargepoint *chargepoint.Client

	mu struct {
		sync.Mutex

		charging bool

		gauges map[string]prometheus.Gauge
	}
}

func pollTime(data VehicleData) time.Duration {
	if !data.VehicleState.Locked && (data.DriveState.ShiftState == nil || data.DriveState.ShiftState == "P" || data.DriveState.ShiftState == "R") && !data.ChargeState.ChargePortDoorOpen {
		return *activePollTime
	}
	if data.DriveState.ShiftState == "D" || data.DriveState.ShiftState == "R" || data.DriveState.ShiftState == "N" || data.ClimateState.IsClimateOn {
		return *drivePollTime
	}
	return *standbyPollTime
}

func (r *RiceLa) startNearbyCharging(ctx context.Context, data tesla.DriveState) error {
	log.Println("starting charging")
	latlng := s2.LatLngFromDegrees(data.Latitude, data.Longitude)
	for _, charger := range knownChargers {
		if charger.DistanceInMeters(latlng) < 20 {
			return charger.Start(ctx, r)
		}
	}
	return nil
}

func (r *RiceLa) stopCharging(ctx context.Context) error {
	log.Println("stop charging")
	userStatus, err := r.chargepoint.UserStatus(ctx)
	log.Printf("Charge Point user status %+v", userStatus)
	if err != nil {
		return err
	}
	for _, station := range userStatus.Charging.Stations {
		if err := r.chargepoint.StopSession(ctx, userStatus.Charging.SessionID, station.DeviceID); err != nil {
			return err
		}
	}
	return nil
}

func (r *RiceLa) setCharging(charging bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.mu.charging = charging
}

func (r *RiceLa) charging() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.mu.charging
}

func (r *RiceLa) monitorVehicle(ctx context.Context, v *tesla.Vehicle) error {
	var data, prevData *VehicleData
	for {
		b := backoff.NewExponentialBackOff()
		b.MaxElapsedTime = 1 * time.Minute
		if err := backoff.Retry(func() error {
			var err error
			data, err = r.getVehicleData(ctx, v)
			if err != nil {
				log.Printf("got error polling (likely retrying) %+v", err)
			}
			return err
		}, b); err != nil {
			return err
		}

		pilotCurrent, _ := data.ChargeState.ChargerPilotCurrent.(float64)
		if data.ChargeState.ChargingState == StateComplete && pilotCurrent > 1 {
			if err := r.stopCharging(ctx); err != nil {
				return err
			}
		}

		if prevData != nil && !prevData.ChargeState.ChargePortDoorOpen && data.ChargeState.ChargePortDoorOpen {
			if err := r.startNearbyCharging(ctx, data.DriveState); err != nil {
				return err
			}
		}

		r.setCharging(data.ChargeState.ChargingState == StateCharging)

		prevData = data

		select {
		case <-ctx.Done():
			return nil
		case <-time.NewTimer(pollTime(*data)).C:
		}
	}
}

type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	CreatedAt   int64  `json:"created_at"`
}

func (r *RiceLa) pollCarServer() error {
	res, err := http.Get(*carServerAddr)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return errors.Errorf("%s", res.Status)
	}

	out := map[string]interface{}{}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return err
	}
	spew.Dump(out)

	r.processCounter("carserver", out)
	return nil
}

func (r *RiceLa) run() error {
	r.mu.gauges = map[string]prometheus.Gauge{}

	eg, ctx := errgroup.WithContext(context.Background())

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	tokenJSON := os.Getenv("TESLA_TOKEN_JSON")
	var token Token
	if err := json.Unmarshal([]byte(tokenJSON), &token); err != nil {
		return err
	}

	var err error
	r.client, err = tesla.NewClientWithToken(
		&tesla.Auth{
			ClientID:     os.Getenv("TESLA_CLIENT_ID"),
			ClientSecret: os.Getenv("TESLA_CLIENT_SECRET"),
			Email:        os.Getenv("TESLA_USERNAME"),
			Password:     os.Getenv("TESLA_PASSWORD"),
		}, &tesla.Token{
			AccessToken: token.AccessToken,
			TokenType:   token.TokenType,
			ExpiresIn:   int(token.ExpiresIn),
			Expires:     token.CreatedAt + token.ExpiresIn,
		})
	if err != nil {
		log.Printf("%+v", errors.Wrapf(err, "failed to create client"))
	}

	r.chargepoint = &chargepoint.Client{
		Token: os.Getenv("CHARGEPOINT_TOKEN"),
	}

	if r.client != nil {
		log.Printf("Tesla token: %+v", r.client.Token)
		eg.Go(func() error {
			vehicles, err := r.client.Vehicles()
			if err != nil {
				return errors.Wrapf(err, "failed to get vehicles")
			}
			for _, v := range vehicles {
				v := v
				eg.Go(func() error {
					return r.monitorVehicle(ctx, v.Vehicle)
				})
			}
			return nil
		})
	}

	eg.Go(func() error {
		return sysmetrics.Monitor(ctx, *drivePollTime)
	})

	eg.Go(func() error {
		for {
			sessions, err := r.chargepoint.GetSessions(ctx)
			if err != nil {
				log.Println("chargpoint stats error", err)
			}
			if len(sessions) > 0 {
				lastSession := sessions[len(sessions)-1]
				r.setCounter("chargepoint:latest:total_amount", lastSession.TotalAmount)
				r.setCounter("chargepoint:latest:miles_added", lastSession.MilesAdded)
				r.setCounter("chargepoint:latest:energy_kwh", lastSession.EnergyKwh)
				r.setCounter("chargepoint:latest:power_kw", lastSession.PowerKw)
				r.setCounter("chargepoint:latest:latitude", lastSession.Lat)
				r.setCounter("chargepoint:latest:longitude", lastSession.Lon)

				if lastSession.CurrentCharging == chargepoint.ChargingFullyCharged {
					if err := r.stopCharging(ctx); err != nil {
						log.Printf("failed to stop charging: %+v", err)
					}
				}
			}

			var totalAmount, milesAdded, energyKwh float64
			for _, session := range sessions {
				totalAmount += session.TotalAmount
				milesAdded += session.MilesAdded
				energyKwh += session.EnergyKwh
			}

			r.setCounter("chargepoint:total_amount", totalAmount)
			r.setCounter("chargepoint:miles_added", milesAdded)
			r.setCounter("chargepoint:energy_kwh", energyKwh)

			select {
			case <-ctx.Done():
				return nil
			case <-time.NewTimer(*chargePointPollTime).C:
			}
		}
	})

	eg.Go(func() error {
		for {
			if err := r.pollCarServer(); err != nil {
				log.Printf("car server error %+v", err)
			}

			select {
			case <-ctx.Done():
				return nil
			case <-time.NewTimer(*drivePollTime).C:
			}
		}
	})

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
