package openrtb_ext

// ExtImpTeal defines the contract for bidrequest.imp[i].ext.prebid.bidder.teal
type ExtImpTeal struct {
	Account   string `json:"account"`
	Placement string `json:"placement,omitempty"`
}
