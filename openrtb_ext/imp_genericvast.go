package openrtb_ext

// ExtImpGenericVast defines the contract for bidrequest.imp[i].ext.prebid.bidder.genericvast.
type ExtImpGenericVast struct {
	URL string   `json:"url"`
	CPM *float64 `json:"cpm,omitempty"`
}
