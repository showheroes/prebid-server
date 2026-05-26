package genericvast

import (
	"fmt"
	"net/http"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v3/adapters"
	"github.com/prebid/prebid-server/v3/config"
	"github.com/prebid/prebid-server/v3/errortypes"
	"github.com/prebid/prebid-server/v3/openrtb_ext"
	"github.com/prebid/prebid-server/v3/util/jsonutil"
)

type adapter struct {
}

// impExt is the relevant subset of imp.ext for this adapter: only the
// `bidder` block carrying the genericvast params. Decoded in a single
// jsonutil.Unmarshal pass.
type impExt struct {
	Bidder openrtb_ext.ExtImpGenericVast `json:"bidder"`
}

const defaultCurrency = "EUR"

// Builder builds a new instance of the Generic VAST adapter for the given bidder with the given config.
func Builder(bidderName openrtb_ext.BidderName, cfg config.Adapter, server config.Server) (adapters.Bidder, error) {
	return &adapter{}, nil
}

// MakeRequests issues exactly one GET to the URL declared on imp[0].ext.bidder, regardless of
// the number of impressions in the request. Headers carry forward device/site/user context.
func (a *adapter) MakeRequests(request *openrtb2.BidRequest, _ *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {
	if len(request.Imp) == 0 {
		return nil, []error{&errortypes.BadInput{Message: "request contains no impressions"}}
	}

	var ext impExt
	err := jsonutil.Unmarshal(request.Imp[0].Ext, &ext)
	if err != nil {
		return nil, []error{err}
	}
	return []*adapters.RequestData{
		{
			Method:  http.MethodGet,
			Uri:     ext.Bidder.URL,
			Headers: buildHeaders(request),
			ImpIDs:  openrtb_ext.GetImpIDs(request.Imp),
		},
	}, nil
}

func buildHeaders(request *openrtb2.BidRequest) http.Header {
	h := http.Header{}
	h.Set("Accept", "application/xml, text/xml, */*")

	if request.Device != nil {
		d := request.Device
		if d.UA != "" {
			h.Set("User-Agent", d.UA)
		}
		ip := d.IP
		if ip == "" {
			ip = d.IPv6
		}
		if ip != "" {
			h.Set("X-Forwarded-For", ip)
			h.Set("X-Device-IP", ip)
			h.Set("X-Real-IP", ip)
		}
		if d.Language != "" {
			h.Set("Accept-Language", d.Language)
			h.Set("X-Device-Language", d.Language)
		}
		if d.DNT != nil {
			h.Set("DNT", fmt.Sprintf("%d", *d.DNT))
		}
	}

	if request.Site != nil {
		ref := request.Site.Page
		if ref == "" {
			ref = request.Site.Ref
		}
		if ref != "" {
			h.Set("Referer", ref)
			h.Set("X-Device-Referer", ref)
		}
	}

	return h
}

// MakeBids parses a VAST response and emits one bid per <Ad> element. Ads are mapped to the
// request's imp IDs positionally with wrap-around: Ad[i] -> imp[i % N].
func (a *adapter) MakeBids(request *openrtb2.BidRequest, _ *adapters.RequestData, response *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, []error{&errortypes.BadServerResponse{
			Message: fmt.Sprintf("unexpected status code: %d", response.StatusCode),
		}}
	}

	if len(response.Body) == 0 {
		return nil, nil
	}

	doc, err := parseVAST(response.Body)
	if err != nil {
		return nil, []error{&errortypes.BadServerResponse{
			Message: fmt.Sprintf("failed to parse VAST response: %v", err),
		}}
	}
	if len(doc.Ads) == 0 {
		return nil, nil
	}

	var perAdFallback float64
	var ext impExt
	if err := jsonutil.Unmarshal(request.Imp[0].Ext, &ext); err == nil {
		perAdFallback = ext.Bidder.CPM / float64(len(doc.Ads))
	}
	resp := &adapters.BidderResponse{Currency: defaultCurrency, Bids: make([]*adapters.TypedBid, 0, len(doc.Ads))}
	currencySet := false

	for i := range doc.Ads {
		ad := &doc.Ads[i]
		impID := request.Imp[i%len(request.Imp)].ID

		price, currency, ok := extractPrice(ad)
		if !ok {
			price = perAdFallback
			currency = defaultCurrency
		}
		if !currencySet && currency != "" {
			resp.Currency = currency
			currencySet = true
		}

		bidID := ad.ID
		if bidID == "" {
			bidID = fmt.Sprintf("genericvast-%s-%d", impID, i)
		}

		resp.Bids = append(resp.Bids, &adapters.TypedBid{
			BidType: openrtb_ext.BidTypeVideo,
			Bid: &openrtb2.Bid{
				ID:      bidID,
				ImpID:   impID,
				Price:   price,
				AdM:     reemitAd(doc.Version, ad),
				CrID:    extractCreativeID(ad),
				Dur:     extractDurationSeconds(ad),
				ADomain: extractAdDomains(ad),
				MType:   openrtb2.MarkupVideo,
			},
		})
	}

	return resp, nil
}
