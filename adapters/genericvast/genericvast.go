package genericvast

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
	"github.com/prebid/prebid-server/v4/config"
	"github.com/prebid/prebid-server/v4/errortypes"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/prebid/prebid-server/v4/util/jsonutil"
)

type adapter struct {
	// fetch retrieves a wrapped VAST document during unwrapping; injectable for tests.
	fetch vastFetcher
	// now supplies the current time; injectable so tests stay deterministic.
	now func() time.Time
}

// auctionStartHeader carries the MakeRequests auction time (unix millis) so MakeBids
// can derive the remaining tmax budget for unwrapping. It is an opaque timestamp.
const auctionStartHeader = "X-Pbs-Auction-Start"

// impExt is the relevant subset of imp.ext for this adapter: only the
// `bidder` block carrying the genericvast params. Decoded in a single
// jsonutil.Unmarshal pass.
type impExt struct {
	Bidder openrtb_ext.ExtImpGenericVast `json:"bidder"`
}

const defaultCurrency = "EUR"

// Builder builds a new instance of the Generic VAST adapter for the given bidder with the given config.
func Builder(bidderName openrtb_ext.BidderName, cfg config.Adapter, server config.Server) (adapters.Bidder, error) {
	return &adapter{fetch: newHTTPFetcher(&http.Client{}), now: time.Now}, nil
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

	uri, err := encodeURL(ext.Bidder.URL)
	if err != nil {
		return nil, []error{err}
	}
	headers := buildHeaders(request)
	headers.Set(auctionStartHeader, strconv.FormatInt(a.now().UnixMilli(), 10))
	return []*adapters.RequestData{
		{
			Method:  http.MethodGet,
			Uri:     uri,
			Headers: headers,
			ImpIDs:  openrtb_ext.GetImpIDs(request.Imp),
		},
	}, nil
}

// encodeURL parses the raw URL and re-encodes its query params
func encodeURL(raw string) (string, error) {
	endpoint, queryParamsRaw, ok := strings.Cut(raw, "?")
	if !ok {
		return raw, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", &errortypes.BadInput{Message: fmt.Sprintf("invalid url %q: %v", raw, err)}
	}

	query := url.Values{}
	for queryParam := range strings.SplitSeq(queryParamsRaw, "&") {
		k, v, _ := strings.Cut(queryParam, "=")
		if k == "" {
			continue
		}
		if decoded, err := url.QueryUnescape(v); err == nil {
			v = decoded
		}
		query.Add(k, v)
	}

	u.RawQuery = query.Encode()

	return u.String(), nil
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
// request's imp IDs positionally with wrap-around: Ad[i] -> imp[i % N]. When the imp opts into
// unwrapping, <Wrapper> ads are resolved to their <InLine> before being emitted.
func (a *adapter) MakeBids(request *openrtb2.BidRequest, externalRequest *adapters.RequestData, response *adapters.ResponseData) (*adapters.BidderResponse, []error) {
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

	var ext impExt
	if err := jsonutil.Unmarshal(request.Imp[0].Ext, &ext); err != nil {
		return nil, []error{err}
	}

	var deadline time.Time
	if ext.Bidder.Unwrap {
		deadline = unwrapDeadline(request.TMax, externalRequest.Headers)
	}

	return collectBidderResponse(a.buildBids(request, doc, ext.Bidder, deadline)), nil
}

// adResult pairs a built bid with its currency so results can be collected in Ad order
// after concurrent unwrapping. A nil bid means the Ad was dropped.
type adResult struct {
	bid      *adapters.TypedBid
	currency string
}

// buildBids builds one result per <Ad>, unwrapping Wrapper ads concurrently since each
// may issue its own chain of network fetches. Non-wrapper ads are cheap and run inline.
func (a *adapter) buildBids(request *openrtb2.BidRequest, doc *vastDoc, params openrtb_ext.ExtImpGenericVast, deadline time.Time) []adResult {
	fwdHeaders := buildHeaders(request)
	var fallback float64
	if len(doc.Ads) > 0 {
		fallback = params.CPM / float64(len(doc.Ads))
	}

	results := make([]adResult, len(doc.Ads))
	var wg sync.WaitGroup
	for i := range doc.Ads {
		ad := &doc.Ads[i]
		impID := request.Imp[i%len(request.Imp)].ID
		unwrap := params.Unwrap && ad.Wrapper != nil

		if !unwrap {
			results[i] = a.buildBid(ad, impID, i, doc.Version, fwdHeaders, deadline, fallback, false)
			continue
		}
		wg.Go(func() {
			results[i] = a.buildBid(ad, impID, i, doc.Version, fwdHeaders, deadline, fallback, true)
		})
	}
	wg.Wait()
	return results
}

// collectBidderResponse assembles the ordered, non-dropped bids and adopts the first
// declared currency.
func collectBidderResponse(results []adResult) *adapters.BidderResponse {
	resp := &adapters.BidderResponse{Currency: defaultCurrency, Bids: make([]*adapters.TypedBid, 0, len(results))}
	currencySet := false
	for _, r := range results {
		if r.bid == nil {
			continue
		}
		if !currencySet && r.currency != "" {
			resp.Currency = r.currency
			currencySet = true
		}
		resp.Bids = append(resp.Bids, r.bid)
	}
	return resp
}

// buildBid constructs a single result for one <Ad>, unwrapping it first when requested.
// It returns a nil bid when the unwrap chain must be dropped.
func (a *adapter) buildBid(ad *vastAd, impID string, i int, version string, headers http.Header, deadline time.Time, fallback float64, unwrap bool) adResult {
	var adm string
	var fields *vastAd
	if unwrap {
		merged, ok := unwrapDocument(a.fetch, &vastDoc{Version: version, Ads: []vastAd{*ad}}, headers, deadline)
		if !ok {
			return adResult{}
		}
		mdoc, err := parseVAST([]byte(merged))
		if err != nil || len(mdoc.Ads) == 0 {
			return adResult{}
		}
		adm = merged
		fields = &mdoc.Ads[0]
	} else {
		adm = reemitAd(version, ad)
		fields = ad
	}

	// Price is taken only from the first fetched VAST, never from unwrapped hops.
	price, currency, ok := extractPrice(ad)
	if !ok {
		price = fallback
		currency = defaultCurrency
	}

	bidID := fields.ID
	if bidID == "" {
		bidID = fmt.Sprintf("genericvast-%s-%d", impID, i)
	}

	return adResult{
		bid: &adapters.TypedBid{
			BidType: openrtb_ext.BidTypeVideo,
			Bid: &openrtb2.Bid{
				ID:      bidID,
				ImpID:   impID,
				Price:   price,
				AdM:     adm,
				CrID:    extractCreativeID(fields),
				Dur:     extractDurationSeconds(fields),
				ADomain: extractAdDomains(fields),
				MType:   openrtb2.MarkupVideo,
			},
		},
		currency: currency,
	}
}
