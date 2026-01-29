package telaria

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"text/template"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v3/adapters"
	"github.com/prebid/prebid-server/v3/config"
	"github.com/prebid/prebid-server/v3/errortypes"
	"github.com/prebid/prebid-server/v3/macros"
	"github.com/prebid/prebid-server/v3/openrtb_ext"
	"github.com/prebid/prebid-server/v3/util/jsonutil"
)

const bidderCurrency = "USD"

type TelariaAdapter struct {
	endpointTemplate *template.Template
}

// This will be part of imp[i].ext when this adapter calls out the Telaria Ad Server
type ImpressionExtOut struct {
	OriginalTagID       string `json:"originalTagid"`
	OriginalPublisherID string `json:"originalPublisherid"`
}

type telariaBidExt struct {
	Extra json.RawMessage `json:"extra,omitempty"`
}

// Checker method to ensure len(request.Imp) > 0
func (a *TelariaAdapter) CheckHasImps(request *openrtb2.BidRequest) error {
	if len(request.Imp) == 0 {
		err := &errortypes.BadInput{
			Message: "Telaria: Missing Imp Object",
		}
		return err
	}
	return nil
}

// Fetches the populated header object
func GetHeaders(request *openrtb2.BidRequest) *http.Header {
	headers := http.Header{}
	headers.Add("Content-Type", "application/json;charset=utf-8")
	headers.Add("Accept", "application/json")
	headers.Add("X-Openrtb-Version", "2.5")

	if request.Device != nil {
		if len(request.Device.UA) > 0 {
			headers.Add("User-Agent", request.Device.UA)
		}

		if len(request.Device.IP) > 0 {
			headers.Add("X-Forwarded-For", request.Device.IP)
		}

		if len(request.Device.Language) > 0 {
			headers.Add("Accept-Language", request.Device.Language)
		}

		if request.Device.DNT != nil {
			headers.Add("Dnt", strconv.Itoa(int(*request.Device.DNT)))
		}
	}

	return &headers
}

// Checks the imp[i].ext object and returns a imp.ext object as per ExtImpTelaria format
func (a *TelariaAdapter) FetchTelariaExtImpParams(imp *openrtb2.Imp) (*openrtb_ext.ExtImpTelaria, error) {
	var bidderExt adapters.ExtImpBidder
	err := jsonutil.Unmarshal(imp.Ext, &bidderExt)

	if err != nil {
		err = &errortypes.BadInput{
			Message: "Telaria: ext.bidder not provided",
		}

		return nil, err
	}

	var telariaExt openrtb_ext.ExtImpTelaria
	err = jsonutil.Unmarshal(bidderExt.Bidder, &telariaExt)

	if err != nil {
		return nil, err
	}

	if telariaExt.SeatCode == "" {
		return nil, &errortypes.BadInput{Message: "Telaria: Seat Code required"}
	}

	return &telariaExt, nil
}

// Method to fetch the original publisher ID. Note that this method must be called
// before we replace publisher.ID with seatCode
func (a *TelariaAdapter) FetchOriginalPublisherID(request *openrtb2.BidRequest) string {

	if request.Site != nil && request.Site.Publisher != nil {
		return request.Site.Publisher.ID
	} else if request.App != nil && request.App.Publisher != nil {
		return request.App.Publisher.ID
	}

	return ""
}

// Method to do a deep copy of the publisher object. It also adds the seatCode as publisher.ID
func (a *TelariaAdapter) MakePublisherObject(seatCode string, publisher *openrtb2.Publisher) *openrtb2.Publisher {
	var pub = &openrtb2.Publisher{ID: seatCode}

	if publisher != nil {
		pub.Domain = publisher.Domain
		pub.Name = publisher.Name
		pub.Cat = publisher.Cat
		pub.Ext = publisher.Ext
	}

	return pub
}

// This method changes <site/app>.publisher.id to the seatCode
func (a *TelariaAdapter) PopulatePublisherId(request *openrtb2.BidRequest, seatCode string) (*openrtb2.Site, *openrtb2.App) {
	if request.Site != nil {
		siteCopy := *request.Site
		siteCopy.Publisher = a.MakePublisherObject(seatCode, request.Site.Publisher)
		return &siteCopy, nil
	} else if request.App != nil {
		appCopy := *request.App
		appCopy.Publisher = a.MakePublisherObject(seatCode, request.App.Publisher)
		return nil, &appCopy
	}
	return nil, nil
}

func (a *TelariaAdapter) MakeRequests(requestIn *openrtb2.BidRequest, reqInfo *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {

	// make a copy of the incoming request
	request := *requestIn

	// ensure that the request has Impressions
	if noImps := a.CheckHasImps(&request); noImps != nil {
		return nil, []error{noImps}
	}

	// clear out any specified currency
	request.Cur = nil

	originalPublisherID := a.FetchOriginalPublisherID(&request)

	headers := GetHeaders(&request)
	var requestData []*adapters.RequestData
	for _, imp := range request.Imp {
		if imp.Banner != nil {
			return nil, []error{&errortypes.BadInput{
				Message: "Telaria: Banner not supported",
			}}
		}
		if imp.Video == nil {
			return nil, []error{&errortypes.BadInput{
				Message: "Telaria: Only video inventory is supported",
			}}
		}

		copyRequest := request
		// fetch adCode & seatCode from imp[i].ext
		telariaImpExt, err := a.FetchTelariaExtImpParams(&imp)
		if err != nil {
			return nil, []error{err}
		}
		if telariaImpExt == nil {
			return nil, []error{&errortypes.BadInput{Message: "Telaria: nil ExtImpTelaria object"}}
		}

		// move the original tagId and the original publisher.id into the imp[i].ext object
		imp.Ext, err = json.Marshal(&ImpressionExtOut{imp.TagID, originalPublisherID})
		if err != nil {
			return nil, []error{err}
		}

		seatCode := telariaImpExt.SeatCode

		if telariaImpExt.SspID == "" {
			telariaImpExt.SspID = "ads"
		}
		// resolve the macros in the endpoint template
		endpoint, err := macros.ResolveMacros(a.endpointTemplate, telariaImpExt)
		if err != nil {
			return nil, []error{&errortypes.BadServerResponse{Message: fmt.Sprintf("Error resolving macros: %v", err)}}
		}

		// Swap the tagID with adCode
		imp.TagID = telariaImpExt.AdCode

		// Add the Extra from Imp to the top level Ext
		if telariaImpExt.Extra != nil {
			copyRequest.Ext, err = json.Marshal(&telariaBidExt{Extra: telariaImpExt.Extra})
			if err != nil {
				return nil, []error{err}
			}
		}

		resolvedBidFloor, err := resolveBidFloor(imp.BidFloor, imp.BidFloorCur, reqInfo)
		if err != nil {
			return nil, []error{err}
		}
		// force USD
		imp.BidFloor = resolvedBidFloor
		imp.BidFloorCur = ""
		if imp.BidFloor > 0.0 {
			imp.BidFloorCur = bidderCurrency
		}

		copyRequest.Imp = []openrtb2.Imp{imp}
		// Add seatCode to <Site/App>.Publisher.ID
		siteObject, appObject := a.PopulatePublisherId(&copyRequest, seatCode)

		copyRequest.Site = siteObject
		copyRequest.App = appObject
		reqJSON, err := json.Marshal(copyRequest)
		if err != nil {
			return nil, []error{err}
		}
		requestData = append(requestData, &adapters.RequestData{
			Method:  "POST",
			Uri:     endpoint,
			Body:    reqJSON,
			Headers: *headers,
			ImpIDs:  openrtb_ext.GetImpIDs(copyRequest.Imp),
		})
	}

	return requestData, nil
}

// resolveBidFloor function returns converted price for the bidfloor, if the incoming request is not in USD. It's a copy from the 'rubicon' bidder
func resolveBidFloor(bidFloor float64, bidFloorCur string, reqInfo *adapters.ExtraRequestInfo) (float64, error) {
	if bidFloor > 0 && bidFloorCur != "" && bidFloorCur != bidderCurrency {
		return reqInfo.ConvertCurrency(bidFloor, bidFloorCur, bidderCurrency)
	}

	return bidFloor, nil
}

func (a *TelariaAdapter) CheckResponseStatusCodes(response *adapters.ResponseData) error {
	if response.StatusCode == http.StatusNoContent {
		return &errortypes.BadInput{Message: "Telaria: Invalid Bid Request received by the server"}
	}

	if response.StatusCode == http.StatusBadRequest {
		return &errortypes.BadInput{
			Message: fmt.Sprintf("Telaria: Unexpected status code: [ %d ] ", response.StatusCode),
		}
	}

	if response.StatusCode == http.StatusServiceUnavailable {
		return &errortypes.BadInput{
			Message: fmt.Sprintf("Telaria: Something went wrong, please contact your Account Manager. Status Code: [ %d ] ", response.StatusCode),
		}
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &errortypes.BadInput{
			Message: fmt.Sprintf("Telaria: Something went wrong, please contact your Account Manager. Status Code: [ %d ] ", response.StatusCode),
		}
	}

	return nil
}

func (a *TelariaAdapter) MakeBids(internalRequest *openrtb2.BidRequest, externalRequest *adapters.RequestData, response *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	httpStatusError := a.CheckResponseStatusCodes(response)
	if httpStatusError != nil {
		return nil, []error{httpStatusError}
	}

	responseBody := response.Body

	var bidResp openrtb2.BidResponse
	if err := jsonutil.Unmarshal(responseBody, &bidResp); err != nil {
		return nil, []error{&errortypes.BadServerResponse{
			Message: "Telaria: Bad Server Response",
		}}
	}

	bidResponse := adapters.NewBidderResponseWithBidsCapacity(len(bidResp.SeatBid[0].Bid))
	sb := bidResp.SeatBid[0]

	for i := range sb.Bid {
		bid := sb.Bid[i]
		bidResponse.Bids = append(bidResponse.Bids, &adapters.TypedBid{
			Bid:     &bid,
			BidType: openrtb_ext.BidTypeVideo,
		})
	}
	return bidResponse, nil
}

// Builder builds a new instance of the Telaria adapter for the given bidder with the given config.
func Builder(bidderName openrtb_ext.BidderName, config config.Adapter, server config.Server) (adapters.Bidder, error) {
	templ, err := template.New("endpointTemplate").Parse(config.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("unable to parse endpoint url template: %v", err)
	}

	return &TelariaAdapter{
		endpointTemplate: templ,
	}, nil
}
