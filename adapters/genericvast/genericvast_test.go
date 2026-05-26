package genericvast

import (
	"encoding/json"
	"testing"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v3/adapters"
	"github.com/prebid/prebid-server/v3/adapters/adapterstest"
	"github.com/prebid/prebid-server/v3/config"
	"github.com/prebid/prebid-server/v3/openrtb_ext"
)

func TestJsonSamples(t *testing.T) {
	bidder, err := Builder(openrtb_ext.BidderGenericVast, config.Adapter{}, config.Server{})
	if err != nil {
		t.Fatalf("Builder returned unexpected error %v", err)
	}

	adapterstest.RunJSONBidderTest(t, "genericvasttest", &xmlBodyBidder{Bidder: bidder})
}

// xmlBodyBidder wraps a real Bidder for the JSON-fixture test harness. Real
// genericvast servers respond with raw XML, but the standard harness stores
// httpResponse.body as a JSON-encoded string (json.RawMessage keeps the
// surrounding quotes). This wrapper unwraps that JSON string before delegating
// to MakeBids, so the production adapter can assume raw XML on the wire.
type xmlBodyBidder struct {
	adapters.Bidder
}

func (b *xmlBodyBidder) MakeBids(req *openrtb2.BidRequest, ext *adapters.RequestData, resp *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	if resp != nil {
		resp = &adapters.ResponseData{
			StatusCode: resp.StatusCode,
			Body:       unwrapJSONStringBody(resp.Body),
			Headers:    resp.Headers,
		}
	}
	return b.Bidder.MakeBids(req, ext, resp)
}

// unwrapJSONStringBody decodes a JSON-encoded string body produced by the JSON
// test-fixture harness back into its raw XML bytes. Non-JSON-string inputs
// (which is what a real bidder would send) are returned unchanged.
func unwrapJSONStringBody(body []byte) []byte {
	trimmed := body
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\r' || trimmed[0] == '\n') {
		trimmed = trimmed[1:]
	}
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return body
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return body
	}
	return []byte(s)
}
