package chargepoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
)

const (
	AccountEndpoint  = "https://account.chargepoint.com/account/v1"
	SessionAckPath   = "/driver/station/session/ack"
	SessionStartPath = "/driver/station/startsession"
	SessionStopPath  = "/driver/station/stopsession"
	MapProdEndpoint  = "https://mc.chargepoint.com/map-prod/v2"
)

const (
	ChargingDone         = "done"
	ChargingFullyCharged = "fully_charged"
)

type Client struct {
	Token string
}

type ErrorResponse struct {
	ID       int    `json:"errorId"`
	Category string `json:"errorCategory"`
	Message  string `json:"errorMessage"`
}

func (e ErrorResponse) Error() string {
	return fmt.Sprintf("%s (Category %s, ID %d)", e.Message, e.Category, e.ID)
}

type DeviceData struct {
	Manufacturer       string `json:"manufacturer"`
	Model              string `json:"model"`
	NotificationID     string `json:"notificationId"`
	NotificationIDType string `json:"notificationIdType"`
	Type               string `json:"type"`
	UDID               string `json:"udid"`
	Version            string `json:"version"`
}

type StartSessionRequest struct {
	DeviceData DeviceData `json:"deviceData"`
	DeviceID   int64      `json:"deviceId"`
}

type StopSessionRequest struct {
	DeviceID  int64 `json:"deviceId"`
	SessionID int64 `json:"sessionId"`
}

type StartSessionResponse struct {
	AckID            int64 `json:"ackId"`
	PurposeFinalized bool  `json:"purposeFinalized"`
}

type SessionAckRequest struct {
	AckID  int64  `json:"ackId"`
	Action string `json:"action"`
}

type SessionAckResponse struct {
	SessionID int64 `json:"sessionId"`
}

type ChargingSession struct {
	Country                 string  `json:"country"`
	MilesAddedPerHour       float64 `json:"miles_added_per_hour,omitempty"`
	City                    string  `json:"city"`
	Purpose                 string  `json:"purpose"`
	PowerKwDisplay          string  `json:"power_kw_display"`
	IsPurposeFinalized      bool    `json:"is_purpose_finalized"`
	UpdatePeriod            int     `json:"update_period"`
	Lon                     float64 `json:"lon"`
	PowerKw                 float64 `json:"power_kw"`
	SessionTime             int     `json:"session_time"`
	PaymentCompleted        bool    `json:"payment_completed"`
	EnergyKwh               float64 `json:"energy_kwh"`
	DeviceName              string  `json:"device_name"`
	APIFlag                 bool    `json:"api_flag"`
	OutletNumber            int     `json:"outlet_number"`
	StateName               string  `json:"state_name"`
	OrganizationCurrency    string  `json:"organization_currency"`
	CurrencyIsoCode         string  `json:"currency_iso_code"`
	CurrentCharging         string  `json:"current_charging"`
	VehicleID               int     `json:"vehicle_id"`
	Lat                     float64 `json:"lat"`
	PortLevel               int     `json:"port_level"`
	ChargingTime            int     `json:"charging_time"`
	DeviceID                int     `json:"device_id"`
	CompanyID               int     `json:"company_id"`
	Address1                string  `json:"address1"`
	EnergyKwhDisplay        string  `json:"energy_kwh_display"`
	SessionID               int     `json:"session_id"`
	IsMfhsEnabled           bool    `json:"is_mfhs_enabled"`
	Zipcode                 string  `json:"zipcode"`
	LastUpdateDataTimestamp int64   `json:"last_update_data_timestamp,omitempty"`
	StartTime               int64   `json:"start_time"`
	PaymentType             string  `json:"payment_type"`
	TotalAmount             float64 `json:"total_amount"`
	CompanyName             string  `json:"company_name"`
	EnableStopCharging      bool    `json:"enable_stop_charging,omitempty"`
	StartOffset             int     `json:"start_offset"`
	MilesAdded              float64 `json:"miles_added"`
	StopChargeSupported     bool    `json:"stop_charge_supported"`
	TotalAmountToUser       float64 `json:"total_amount_to_user,omitempty"`
	EndTime                 int64   `json:"end_time,omitempty"`
}

type ChargingStatusRequest struct {
	ChargingStatus struct {
		Mfhs struct {
		} `json:"mfhs"`
		SessionID int `json:"session_id"`
	} `json:"charging_status"`
	UserID int `json:"user_id"`
}

type ChargingActivityMonthlyRequest struct {
	ChargingActivityMonthly struct {
		Mfhs struct {
		} `json:"mfhs"`
		PageSize int `json:"page_size"`
	} `json:"charging_activity_monthly"`
	UserID int `json:"user_id"`
}

type MapProdError struct {
	ErrorMessage string `json:"error_message"`
	ErrorCode    int    `json:"error_code"`
}

func (e MapProdError) Error() string {
	return e.ErrorMessage
}

type MapProdResponse struct {
	ChargingActivityMonthly struct {
		PrimaryVehicle struct {
			Year  int    `json:"year"`
			Model string `json:"model"`
			Make  string `json:"make"`
		} `json:"primary_vehicle"`
		MonthInfo []struct {
			Sessions  []ChargingSession `json:"sessions"`
			EnergyKwh struct {
				Public int `json:"public"`
			} `json:"energy_kwh"`
			Cost struct {
				Public          int    `json:"public"`
				CurrencyIsoCode string `json:"currency_iso_code"`
			} `json:"cost"`
			Month      int `json:"month"`
			Year       int `json:"year"`
			MilesAdded struct {
				Public int `json:"public"`
			} `json:"miles_added"`
			VehicleInfo map[string]struct {
				Year             int     `json:"year"`
				EvRange          int     `json:"ev_range"`
				IsPrimaryVehicle bool    `json:"is_primary_vehicle"`
				Model            string  `json:"model"`
				BatteryCapacity  float64 `json:"battery_capacity"`
				VehicleID        int     `json:"vehicle_id"`
				Make             string  `json:"make"`
			} `json:"vehicle_info"`
		} `json:"month_info"`
		PageOffset string `json:"page_offset"`
	} `json:"charging_activity_monthly"`
}

type UserStatusResponse struct {
	UserStatus UserStatus `json:"user_status"`
}
type Stations struct {
	DeviceID int64   `json:"deviceId"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Name     string  `json:"name"`
}
type Charging struct {
	CurrentTimeUTC int        `json:"currentTimeUTC"`
	SessionID      int64      `json:"sessionId"`
	StartTimeUTC   int        `json:"startTimeUTC"`
	State          string     `json:"state"`
	Stations       []Stations `json:"stations"`
}
type UserStatus struct {
	Charging Charging `json:"charging"`
}
type UserStatusRequest struct {
	UserStatus struct {
		Mfhs struct {
		} `json:"mfhs"`
	} `json:"user_status"`
}

func (c *Client) UserStatus(ctx context.Context) (UserStatus, error) {
	var resp UserStatusResponse
	if err := c.makeRequest(ctx, MapProdEndpoint, UserStatusRequest{}, &resp); err != nil {
		return UserStatus{}, err
	}
	return resp.UserStatus, nil
}

func (c *Client) GetSessions(ctx context.Context) ([]ChargingSession, error) {
	var req ChargingActivityMonthlyRequest
	//req.UserID = c.UserID
	req.ChargingActivityMonthly.PageSize = 1000

	var resp MapProdResponse
	if err := c.makeRequest(ctx, MapProdEndpoint, req, &resp); err != nil {
		return nil, err
	}

	var sessions []ChargingSession
	for _, month := range resp.ChargingActivityMonthly.MonthInfo {
		sessions = append(sessions, month.Sessions...)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartTime < sessions[j].StartTime
	})
	return sessions, nil
}

func (c *Client) StopSession(ctx context.Context, sessionID, deviceID int64) error {
	var resp struct{}
	if err := c.makeRequest(ctx, AccountEndpoint+SessionStopPath, StopSessionRequest{
		SessionID: sessionID,
		DeviceID:  deviceID,
	}, &resp); err != nil {
		return err
	}
	return nil
}

func (c *Client) StartSession(ctx context.Context, deviceID int64) (int64, error) {
	var resp StartSessionResponse
	if err := c.makeRequest(ctx, AccountEndpoint+SessionStartPath, StartSessionRequest{
		DeviceID: deviceID,
		DeviceData: DeviceData{
			Manufacturer:       "unknown",
			Model:              "unknown",
			NotificationID:     "",
			NotificationIDType: "FCM",
			Type:               "android",
			UDID:               "",
			Version:            "5.60.0-237-1702",
		},
	}, &resp); err != nil {
		return 0, err
	}

	var ackResp SessionAckResponse
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 1 * time.Minute
	if err := backoff.Retry(func() error {
		return c.makeRequest(ctx, AccountEndpoint+SessionAckPath, SessionAckRequest{
			AckID:  resp.AckID,
			Action: "start_session",
		}, &ackResp)
	}, b); err != nil {
		return 0, err
	}

	return ackResp.SessionID, nil
}

func (c *Client) makeRequest(ctx context.Context, targetURL string, request interface{}, response interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	reqBody, err := json.Marshal(request)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Cookie", "coulomb_sess="+url.QueryEscape(c.Token))
	req.Header.Set("cp-session-token", c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// account errors
	var errResp ErrorResponse
	if err := json.Unmarshal(respBody, &errResp); err != nil {
		return err
	}
	if errResp.Message != "" {
		return errors.WithStack(errResp)
	}

	// map prod errors
	errMap := map[string]MapProdError{}
	if err := json.Unmarshal(respBody, &errMap); err != nil {
		if err, ok := err.(*json.UnmarshalTypeError); ok {
		} else {
			return err
		}
	}
	for _, err := range errMap {
		if err.Error() != "" {
			return errors.WithStack(err)
		}
	}

	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	if err := json.Unmarshal(respBody, response); err != nil {
		return err
	}
	return nil
}
