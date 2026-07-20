package openrtb_ext

// ImpTeal defines the contract for bidrequest.imp[i].ext.prebid.bidder.teal
type ImpTeal struct {
	Account   string `json:"account"`
	Placement string `json:"placement,omitempty"`
}
