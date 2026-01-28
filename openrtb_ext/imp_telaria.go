package openrtb_ext

import "encoding/json"

type ExtImpTelaria struct {
	AdCode   string          `json:"adCode,omitempty"`
	SeatCode string          `json:"seatCode"`
	SeatID   string          `json:"seatID,omitempty"`
	Extra    json.RawMessage `json:"extra,omitempty"`
}
