package openrtb_ext

import "encoding/json"

type ExtImpTelaria struct {
	AdCode   string          `json:"adCode,omitempty"`
	SeatCode string          `json:"seatCode"`
	SspID    string          `json:"sspID,omitempty"`
	Extra    json.RawMessage `json:"extra,omitempty"`
}
