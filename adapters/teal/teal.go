package teal

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v3/adapters"
	"github.com/prebid/prebid-server/v3/config"
	"github.com/prebid/prebid-server/v3/errortypes"
	"github.com/prebid/prebid-server/v3/openrtb_ext"
	"github.com/prebid/prebid-server/v3/util/jsonutil"
)

type adapter struct {
	endpoint string
}

// Builder builds a new instance of the Teal adapter for the given bidder with the given config.
func Builder(bidderName openrtb_ext.BidderName, config config.Adapter, server config.Server) (adapters.Bidder, error) {
	return &adapter{
		endpoint: config.Endpoint,
	}, nil
}

func (a *adapter) MakeRequests(request *openrtb2.BidRequest, reqInfo *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {
	var errors []error
	var account string
	modifiedImps := make([]openrtb2.Imp, 0, len(request.Imp))

	for _, imp := range request.Imp {
		tealExt, err := parseImpExt(imp)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		if account == "" {
			account = tealExt.Account
		}

		modifiedImp, err := modifyImp(imp, tealExt.Placement)
		if err != nil {
			errors = append(errors, err)
			continue
		}
		modifiedImps = append(modifiedImps, modifiedImp)
	}

	if len(modifiedImps) == 0 {
		return nil, errors
	}

	ext, err := modifyRequestExt(request.Ext)
	if err != nil {
		return nil, append(errors, err)
	}

	// request is a per-bidder copy, but Site/App (and their Publisher) are
	// pointers shared across bidders, so they are copied before mutation.
	request.Imp = modifiedImps
	request.Site = modifySite(request.Site, account)
	request.App = modifyApp(request.App, account)
	request.Ext = ext

	body, err := json.Marshal(request)
	if err != nil {
		return nil, append(errors, err)
	}

	headers := http.Header{}
	headers.Add("Content-Type", "application/json;charset=utf-8")
	headers.Add("Accept", "application/json")

	return []*adapters.RequestData{{
		Method:  http.MethodPost,
		Uri:     a.endpoint,
		Body:    body,
		Headers: headers,
		ImpIDs:  openrtb_ext.GetImpIDs(request.Imp),
	}}, errors
}

func parseImpExt(imp openrtb2.Imp) (openrtb_ext.ExtImpTeal, error) {
	var bidderExt adapters.ExtImpBidder
	if err := jsonutil.Unmarshal(imp.Ext, &bidderExt); err != nil {
		return openrtb_ext.ExtImpTeal{}, &errortypes.BadInput{
			Message: "Error parsing imp.ext for impression " + imp.ID,
		}
	}

	var tealExt openrtb_ext.ExtImpTeal
	if err := jsonutil.Unmarshal(bidderExt.Bidder, &tealExt); err != nil {
		return openrtb_ext.ExtImpTeal{}, &errortypes.BadInput{
			Message: "Error parsing imp.ext for impression " + imp.ID,
		}
	}

	if err := validateImpExt(tealExt); err != nil {
		return openrtb_ext.ExtImpTeal{}, err
	}

	return tealExt, nil
}

func validateImpExt(ext openrtb_ext.ExtImpTeal) error {
	if strings.TrimSpace(ext.Account) == "" {
		return &errortypes.BadInput{Message: "account parameter failed validation"}
	}

	return nil
}

func modifyImp(imp openrtb2.Imp, placement string) (openrtb2.Imp, error) {
	if placement == "" {
		return imp, nil
	}

	placementValue, err := json.Marshal(placement)
	if err != nil {
		return openrtb2.Imp{}, &errortypes.BadInput{
			Message: "Error modifying imp.ext for impression " + imp.ID,
		}
	}

	modifiedExt, err := jsonparser.Set(imp.Ext, placementValue, "prebid", "storedrequest", "id")
	if err != nil {
		return openrtb2.Imp{}, &errortypes.BadInput{
			Message: "Error modifying imp.ext for impression " + imp.ID,
		}
	}

	imp.Ext = modifiedExt
	return imp, nil
}

func modifySite(site *openrtb2.Site, account string) *openrtb2.Site {
	if site == nil {
		return nil
	}

	siteCopy := *site
	siteCopy.Publisher = modifyPublisher(site.Publisher, account)
	return &siteCopy
}

func modifyApp(app *openrtb2.App, account string) *openrtb2.App {
	if app == nil {
		return nil
	}

	appCopy := *app
	appCopy.Publisher = modifyPublisher(app.Publisher, account)
	return &appCopy
}

func modifyPublisher(publisher *openrtb2.Publisher, account string) *openrtb2.Publisher {
	var publisherCopy openrtb2.Publisher
	if publisher != nil {
		publisherCopy = *publisher
	}

	publisherCopy.ID = account
	return &publisherCopy
}

func modifyRequestExt(ext json.RawMessage) (json.RawMessage, error) {
	if len(ext) == 0 {
		return json.RawMessage(`{"bids":{"pbs":1}}`), nil
	}

	return jsonparser.Set(ext, []byte(`{"pbs":1}`), "bids")
}

func (a *adapter) MakeBids(request *openrtb2.BidRequest, requestData *adapters.RequestData, response *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	if adapters.IsResponseStatusCodeNoContent(response) {
		return nil, nil
	}

	if err := adapters.CheckResponseStatusCodeForErrors(response); err != nil {
		return nil, []error{err}
	}

	var bidResponse openrtb2.BidResponse
	if err := jsonutil.Unmarshal(response.Body, &bidResponse); err != nil {
		return nil, []error{&errortypes.BadServerResponse{Message: err.Error()}}
	}

	bidderResponse := adapters.NewBidderResponseWithBidsCapacity(len(request.Imp))
	bidderResponse.Currency = bidResponse.Cur

	for _, seatBid := range bidResponse.SeatBid {
		for i := range seatBid.Bid {
			bidderResponse.Bids = append(bidderResponse.Bids, &adapters.TypedBid{
				Bid:     &seatBid.Bid[i],
				BidType: getMediaTypeForImp(seatBid.Bid[i].ImpID, request.Imp),
			})
		}
	}

	return bidderResponse, nil
}

func getMediaTypeForImp(impID string, imps []openrtb2.Imp) openrtb_ext.BidType {
	for i := range imps {
		imp := &imps[i]
		if imp.ID != impID {
			continue
		}

		switch {
		case imp.Banner != nil:
			return openrtb_ext.BidTypeBanner
		case imp.Video != nil:
			return openrtb_ext.BidTypeVideo
		case imp.Audio != nil:
			return openrtb_ext.BidTypeAudio
		case imp.Native != nil:
			return openrtb_ext.BidTypeNative
		}
	}

	return openrtb_ext.BidTypeBanner
}
