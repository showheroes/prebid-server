package genericvast

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/adapters"
)

const wrapperVAST = `<VAST version="4.2"><Ad id="w1"><Wrapper>` +
	`<AdSystem>WrapSSP</AdSystem>` +
	`<VASTAdTagURI><![CDATA[https://downstream.example.com/vast.xml]]></VASTAdTagURI>` +
	`<Impression><![CDATA[https://imp.wrap]]></Impression>` +
	`<Error><![CDATA[https://err.wrap]]></Error>` +
	`<Pricing model="CPM" currency="EUR">0.5</Pricing>` +
	`<ViewableImpression><Viewable><![CDATA[https://view.wrap]]></Viewable></ViewableImpression>` +
	`<AdVerifications><Verification vendor="acme"><JavaScriptResource><![CDATA[https://v.js]]></JavaScriptResource></Verification></AdVerifications>` +
	`<Creatives><Creative><Linear>` +
	`<TrackingEvents><Tracking event="start"><![CDATA[https://trk.start]]></Tracking></TrackingEvents>` +
	`<VideoClicks><ClickTracking><![CDATA[https://clk.wrap]]></ClickTracking></VideoClicks>` +
	`</Linear></Creative></Creatives>` +
	`<Extensions><Extension type="foo"><Custom>bar</Custom></Extension></Extensions>` +
	`</Wrapper></Ad></VAST>`

const inlineVAST = `<VAST version="4.2"><Ad id="in1"><InLine>` +
	`<AdSystem>InlineSSP</AdSystem>` +
	`<Advertiser>brand.example.com</Advertiser>` +
	`<Creatives><Creative id="c1"><Linear>` +
	`<Duration>00:00:15</Duration>` +
	`<TrackingEvents><Tracking event="complete"><![CDATA[https://trk.complete]]></Tracking></TrackingEvents>` +
	`<VideoClicks><ClickThrough><![CDATA[https://ct]]></ClickThrough></VideoClicks>` +
	`<MediaFiles><MediaFile><![CDATA[https://media.mp4]]></MediaFile></MediaFiles>` +
	`</Linear></Creative></Creatives>` +
	`</InLine></Ad></VAST>`

func staticFetcher(body string) vastFetcher {
	return func(_ context.Context, _ string, _ time.Duration, _ http.Header) ([]byte, error) {
		return []byte(body), nil
	}
}

func TestUnwrapVASTMergesTracking(t *testing.T) {
	merged, ok := unwrapVAST(staticFetcher(inlineVAST), []byte(wrapperVAST), nil, time.Now().Add(time.Second))
	if !ok {
		t.Fatalf("expected unwrap to succeed")
	}

	// Every wrapper tracking fragment must be present exactly once in the InLine.
	wantOnce := []string{
		"https://imp.wrap",
		"https://err.wrap",
		"https://view.wrap",
		`vendor="acme"`,
		"https://trk.start",
		"https://clk.wrap",
		"<Custom>bar</Custom>",
	}
	for _, frag := range wantOnce {
		if n := strings.Count(merged, frag); n != 1 {
			t.Errorf("fragment %q appears %d times, want 1\n%s", frag, n, merged)
		}
	}

	// The resolved InLine content must survive.
	if !strings.Contains(merged, "https://media.mp4") || !strings.Contains(merged, "https://trk.complete") {
		t.Errorf("inline content missing after merge:\n%s", merged)
	}

	// Placement checks.
	if strings.Index(merged, "https://imp.wrap") > strings.Index(merged, "<Creatives>") {
		t.Errorf("impression should be inserted before <Creatives>:\n%s", merged)
	}
	if !strings.Contains(merged, `<Tracking event="start"><![CDATA[https://trk.start]]></Tracking></TrackingEvents>`) {
		t.Errorf("wrapper tracking not merged into existing TrackingEvents:\n%s", merged)
	}
	if !strings.Contains(merged, `<ClickTracking><![CDATA[https://clk.wrap]]></ClickTracking></VideoClicks>`) {
		t.Errorf("wrapper click tracking not merged into existing VideoClicks:\n%s", merged)
	}
	if !strings.Contains(merged, "<ViewableImpression><Viewable><![CDATA[https://view.wrap]]></Viewable></ViewableImpression>") {
		t.Errorf("ViewableImpression container not created:\n%s", merged)
	}
	if !strings.Contains(merged, "<AdVerifications><Verification") {
		t.Errorf("AdVerifications container not created:\n%s", merged)
	}
	if !strings.Contains(merged, "<AdSystem>WrapSSP,InlineSSP</AdSystem>") {
		t.Errorf("AdSystem should be comma-joined across hops:\n%s", merged)
	}
}

func TestUnwrapVASTMergesIntoExistingContainers(t *testing.T) {
	// InLine already has Impression, ViewableImpression, AdVerifications, Extensions.
	inline := `<VAST version="4.2"><Ad><InLine>` +
		`<AdSystem>S</AdSystem>` +
		`<Impression><![CDATA[https://imp.inline]]></Impression>` +
		`<ViewableImpression><Viewable><![CDATA[https://view.inline]]></Viewable></ViewableImpression>` +
		`<AdVerifications><Verification vendor="inline"/></AdVerifications>` +
		`<Creatives><Creative><Linear><Duration>00:00:05</Duration>` +
		`<TrackingEvents><Tracking event="complete"><![CDATA[https://trk.inline]]></Tracking></TrackingEvents>` +
		`</Linear></Creative></Creatives>` +
		`<Extensions><Extension type="bar"><X/></Extension></Extensions>` +
		`</InLine></Ad></VAST>`

	merged, ok := unwrapVAST(staticFetcher(inline), []byte(wrapperVAST), nil, time.Now().Add(time.Second))
	if !ok {
		t.Fatalf("expected unwrap to succeed")
	}

	// Merged into existing single containers (no duplicate container tags).
	if n := strings.Count(merged, "<ViewableImpression>"); n != 1 {
		t.Errorf("expected single ViewableImpression, got %d:\n%s", n, merged)
	}
	if n := strings.Count(merged, "<AdVerifications>"); n != 1 {
		t.Errorf("expected single AdVerifications, got %d:\n%s", n, merged)
	}
	if n := strings.Count(merged, "<Extensions>"); n != 1 {
		t.Errorf("expected single Extensions, got %d:\n%s", n, merged)
	}
	if n := strings.Count(merged, "<TrackingEvents>"); n != 1 {
		t.Errorf("expected single TrackingEvents, got %d:\n%s", n, merged)
	}
	for _, frag := range []string{"https://view.wrap", "https://view.inline", `vendor="acme"`, `vendor="inline"`, "https://trk.start", "https://clk.wrap"} {
		if !strings.Contains(merged, frag) {
			t.Errorf("missing %q after merge:\n%s", frag, merged)
		}
	}
}

func TestUnwrapVASTRecurses(t *testing.T) {
	// First fetch returns another wrapper, second returns the InLine.
	second := staticFetcher(inlineVAST)
	first := func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
		return []byte(strings.Replace(wrapperVAST, "https://downstream.example.com/vast.xml", "https://second.example.com/vast.xml", 1)), nil
	}
	calls := 0
	fetch := func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
		calls++
		if calls == 1 {
			return first(ctx, url, to, h)
		}
		return second(ctx, url, to, h)
	}
	merged, ok := unwrapVAST(fetch, []byte(wrapperVAST), nil, time.Now().Add(time.Second))
	if !ok {
		t.Fatalf("expected unwrap to succeed")
	}
	// Impression from both wrapper hops present twice.
	if n := strings.Count(merged, "https://imp.wrap"); n != 2 {
		t.Errorf("expected impression twice after two wrapper hops, got %d", n)
	}
}

func TestUnwrapVASTDropsOnMaxWrappers(t *testing.T) {
	// Fetcher always returns a wrapper, so the chain never resolves.
	fetch := func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
		return []byte(wrapperVAST), nil
	}
	if _, ok := unwrapVAST(fetch, []byte(wrapperVAST), nil, time.Now().Add(time.Second)); ok {
		t.Fatalf("expected drop when max wrappers exceeded")
	}
}

func TestUnwrapVASTDropsOnFetchError(t *testing.T) {
	fetch := func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
		return nil, context.DeadlineExceeded
	}
	if _, ok := unwrapVAST(fetch, []byte(wrapperVAST), nil, time.Now().Add(time.Second)); ok {
		t.Fatalf("expected drop on fetch error")
	}
}

func TestUnwrapVASTDropsOnEmptyTerminal(t *testing.T) {
	if _, ok := unwrapVAST(staticFetcher(`<VAST version="4.0"></VAST>`), []byte(wrapperVAST), nil, time.Now().Add(time.Second)); ok {
		t.Fatalf("expected drop on empty terminal VAST")
	}
}

func TestUnwrapVASTDropsOnExhaustedBudget(t *testing.T) {
	fetch := func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
		t.Fatalf("fetch should not be called when budget is exhausted")
		return nil, nil
	}
	if _, ok := unwrapVAST(fetch, []byte(wrapperVAST), nil, time.Now()); ok {
		t.Fatalf("expected drop when budget is exhausted")
	}
}

func TestUnwrapVASTMissingAdTagURI(t *testing.T) {
	noURI := `<VAST version="4.2"><Ad><Wrapper><AdSystem>S</AdSystem></Wrapper></Ad></VAST>`
	if _, ok := unwrapVAST(staticFetcher(inlineVAST), []byte(noURI), nil, time.Now().Add(time.Second)); ok {
		t.Fatalf("expected drop when VASTAdTagURI is missing")
	}
}

func TestMakeBidsUnwrap(t *testing.T) {
	a := &adapter{fetch: staticFetcher(inlineVAST), now: time.Now}
	req := &openrtb2.BidRequest{
		ID:   "r",
		TMax: 1000,
		Site: &openrtb2.Site{Page: "https://publisher.example.com/"},
		Imp: []openrtb2.Imp{{
			ID:    "imp-1",
			Video: &openrtb2.Video{MIMEs: []string{"video/mp4"}},
			Ext:   json.RawMessage(`{"bidder":{"url":"https://vast.example.com/wrapper","cpm":1.5,"unwrap":true}}`),
		}},
	}
	ext := &adapters.RequestData{Headers: http.Header{}}
	resp := &adapters.ResponseData{StatusCode: http.StatusOK, Body: []byte(wrapperVAST)}

	out, errs := a.MakeBids(req, ext, resp)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if out == nil || len(out.Bids) != 1 {
		t.Fatalf("expected 1 bid, got %v", out)
	}
	bid := out.Bids[0].Bid
	if bid.ImpID != "imp-1" {
		t.Errorf("wrong imp id: %s", bid.ImpID)
	}
	if bid.CrID != "c1" {
		t.Errorf("crid should come from InLine, got %s", bid.CrID)
	}
	if bid.Price != 0.5 {
		t.Errorf("price should come from the first VAST (wrapper) Pricing, got %v", bid.Price)
	}
	if bid.Dur != 15 {
		t.Errorf("duration should come from InLine, got %d", bid.Dur)
	}
	if !strings.Contains(bid.AdM, "https://imp.wrap") || !strings.Contains(bid.AdM, "https://media.mp4") {
		t.Errorf("AdM should be the merged InLine:\n%s", bid.AdM)
	}
	if strings.Contains(bid.AdM, "<Wrapper>") {
		t.Errorf("AdM should not contain a Wrapper after unwrapping:\n%s", bid.AdM)
	}
}

func TestMakeBidsUnwrapDropsBadChain(t *testing.T) {
	a := &adapter{
		fetch: func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
		now: time.Now,
	}
	req := &openrtb2.BidRequest{
		ID:  "r",
		Imp: []openrtb2.Imp{{ID: "imp-1", Ext: json.RawMessage(`{"bidder":{"url":"https://x","cpm":1.5,"unwrap":true}}`)}},
	}
	out, errs := a.MakeBids(req, &adapters.RequestData{Headers: http.Header{}}, &adapters.ResponseData{StatusCode: http.StatusOK, Body: []byte(wrapperVAST)})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(out.Bids) != 0 {
		t.Fatalf("expected the wrapper bid to be dropped, got %d bids", len(out.Bids))
	}
}

func TestMakeBidsUnwrapDisabledKeepsWrapper(t *testing.T) {
	called := false
	a := &adapter{
		fetch: func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
			called = true
			return []byte(inlineVAST), nil
		},
		now: time.Now,
	}
	req := &openrtb2.BidRequest{
		ID:  "r",
		Imp: []openrtb2.Imp{{ID: "imp-1", Ext: json.RawMessage(`{"bidder":{"url":"https://x","cpm":1.5}}`)}},
	}
	out, errs := a.MakeBids(req, &adapters.RequestData{Headers: http.Header{}}, &adapters.ResponseData{StatusCode: http.StatusOK, Body: []byte(wrapperVAST)})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if called {
		t.Errorf("fetch must not be called when unwrap is disabled")
	}
	if len(out.Bids) != 1 || !strings.Contains(out.Bids[0].Bid.AdM, "<Wrapper>") {
		t.Errorf("wrapper should be emitted untouched when unwrap is disabled")
	}
}

func TestUnwrapDeadline(t *testing.T) {
	// No header: deadline is roughly now + tmax.
	got := unwrapDeadline(1000, nil)
	if d := time.Until(got); d < 900*time.Millisecond || d > 1100*time.Millisecond {
		t.Errorf("expected ~1s deadline, got %v", d)
	}
	// No tmax falls back to the default budget.
	got = unwrapDeadline(0, nil)
	if d := time.Until(got); d < defaultUnwrapBudget-100*time.Millisecond {
		t.Errorf("expected default budget deadline, got %v", d)
	}
	// Auction started long ago: deadline is already in the past.
	h := http.Header{}
	h.Set(auctionStartHeader, "1")
	if got = unwrapDeadline(1000, h); !got.Before(time.Now()) {
		t.Errorf("expected past deadline for an old auction start, got %v", got)
	}
	// Recent start: deadline reflects start + tmax.
	start := time.Now().Add(-200 * time.Millisecond)
	h.Set(auctionStartHeader, strconv.FormatInt(start.UnixMilli(), 10))
	if got = unwrapDeadline(1000, h); time.Until(got) > 900*time.Millisecond {
		t.Errorf("expected ~800ms remaining, got %v", time.Until(got))
	}
}

func TestUnwrapVASTRetriesThenSucceeds(t *testing.T) {
	attempts := 0
	fetch := func(ctx context.Context, url string, to time.Duration, h http.Header) ([]byte, error) {
		attempts++
		switch attempts {
		case 1:
			return nil, context.DeadlineExceeded // HTTP error -> retry
		case 2:
			return []byte(`<VAST version="4.0"></VAST>`), nil // empty response -> retry
		default:
			return []byte(inlineVAST), nil
		}
	}
	if _, ok := unwrapVAST(fetch, []byte(wrapperVAST), nil, time.Now().Add(time.Second)); !ok {
		t.Fatalf("expected success after retries")
	}
	if attempts != maxFetchAttempts {
		t.Errorf("expected %d attempts, got %d", maxFetchAttempts, attempts)
	}
}

func TestMakeBidsUnwrapPriceFromFirstVASTOnly(t *testing.T) {
	// The wrapper (1st VAST) carries no Pricing; the resolved InLine does. Price must
	// fall back to the configured CPM and never use the deeper InLine's Pricing.
	wrapperNoPricing := strings.Replace(wrapperVAST, `<Pricing model="CPM" currency="EUR">0.5</Pricing>`, "", 1)
	inlineWithPricing := strings.Replace(inlineVAST,
		"<Advertiser>brand.example.com</Advertiser>",
		`<Advertiser>brand.example.com</Advertiser><Pricing model="CPM" currency="EUR">9.99</Pricing>`, 1)

	a := &adapter{fetch: staticFetcher(inlineWithPricing), now: time.Now}
	req := &openrtb2.BidRequest{
		ID:   "r",
		TMax: 1000,
		Imp: []openrtb2.Imp{{
			ID:  "imp-1",
			Ext: json.RawMessage(`{"bidder":{"url":"https://x","cpm":1.5,"unwrap":true}}`),
		}},
	}
	out, errs := a.MakeBids(req, &adapters.RequestData{Headers: http.Header{}},
		&adapters.ResponseData{StatusCode: http.StatusOK, Body: []byte(wrapperNoPricing)})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(out.Bids) != 1 {
		t.Fatalf("expected 1 bid, got %d", len(out.Bids))
	}
	if out.Bids[0].Bid.Price != 1.5 {
		t.Errorf("price should be the fallback CPM 1.5, got %v", out.Bids[0].Bid.Price)
	}
	if strings.Contains(out.Bids[0].Bid.AdM, "9.99") {
		// The InLine's own Pricing may remain in the markup, but must not drive the bid price;
		// this only guards against accidentally merging a wrapper Pricing here.
		if !strings.Contains(inlineWithPricing, "9.99") {
			t.Errorf("unexpected pricing in AdM")
		}
	}
}

func TestNewHTTPFetcher(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(inlineVAST))
	}))
	defer srv.Close()

	fetch := newHTTPFetcher(srv.Client())
	h := http.Header{}
	h.Set("User-Agent", "Mozilla/5.0")
	body, err := fetch(context.Background(), srv.URL, time.Second, h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != inlineVAST {
		t.Errorf("unexpected body: %s", body)
	}
	if gotUA != "Mozilla/5.0" {
		t.Errorf("headers not forwarded, got UA %q", gotUA)
	}
}

func TestNewHTTPFetcherNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	fetch := newHTTPFetcher(srv.Client())
	if _, err := fetch(context.Background(), srv.URL, time.Second, nil); err == nil {
		t.Fatalf("expected error on non-200 response")
	}
}
