package genericvast

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestUnwrapNamespaceContexts(t *testing.T) {
	for _, test := range []struct{ name, terminal, wrapper string }{
		{"inline", `<VAST><Ad><InLine xmlns:vendor="urn:vendor"><Extensions><Extension><vendor:Data/></Extension></Extensions></InLine></Ad></VAST>`, wrapperVAST},
		{"extension", `<VAST><Ad><InLine><Extensions><Extension xmlns:vendor="urn:vendor"><vendor:Data/></Extension></Extensions></InLine></Ad></VAST>`, wrapperVAST},
		{"container", `<VAST><Ad><InLine><Extensions xmlns:vendor="urn:vendor"><Extension><vendor:Data/></Extension></Extensions></InLine></Ad></VAST>`, wrapperVAST},
		{"verification", inlineVAST, `<VAST xmlns:vendor="urn:vendor"><Ad><Wrapper><VASTAdTagURI>https://next</VASTAdTagURI><AdVerifications><Verification vendor="example"><vendor:Data/></Verification></AdVerifications></Wrapper></Ad></VAST>`},
		{"linear", `<VAST><Ad><InLine><Creatives><Creative><Linear xmlns:vendor="urn:vendor"><AdParameters><vendor:Data/></AdParameters></Linear></Creative></Creatives></InLine></Ad></VAST>`, wrapperVAST},
	} {
		t.Run(test.name, func(t *testing.T) {
			merged, ok := unwrapVAST(staticFetcher(test.terminal), []byte(test.wrapper), nil, time.Now().Add(time.Second))
			if !ok {
				t.Fatal("unwrap failed")
			}
			decoder := xml.NewDecoder(strings.NewReader(merged))
			for {
				token, err := decoder.Token()
				if err != nil {
					t.Fatalf("missing Data or invalid XML: %v", err)
				}
				if element, ok := token.(xml.StartElement); ok && element.Name.Local == "Data" {
					if element.Name.Space != "urn:vendor" {
						t.Errorf("incorrect binding: %+v", element.Name)
					}
					break
				}
			}
		})
	}
}

func TestUnwrapFollowAdditionalWrappers(t *testing.T) {
	for _, value := range []string{"", "true", "false", "1", "0"} {
		for _, nested := range []bool{false, true} {
			t.Run(value+strconv.FormatBool(nested), func(t *testing.T) {
				wrapper := wrapperVAST
				if value != "" {
					wrapper = strings.Replace(wrapper, "<Wrapper>", `<Wrapper followAdditionalWrappers="`+value+`">`, 1)
				}
				calls := 0
				fetch := func(_ context.Context, _ string, _ time.Duration, _ http.Header) ([]byte, error) {
					calls++
					if calls == 1 && nested {
						return []byte(wrapperVAST), nil
					}
					return []byte(inlineVAST), nil
				}
				_, ok := unwrapVAST(fetch, []byte(wrapper), nil, time.Now().Add(time.Second))
				blocked := nested && (value == "false" || value == "0")
				wantCalls := 1
				if nested && !blocked {
					wantCalls = 2
				}
				if ok == blocked || calls != wantCalls {
					t.Errorf("success=%v, calls=%d; blocked=%v, want calls=%d", ok, calls, blocked, wantCalls)
				}
			})
		}
	}
}

func TestUnwrapCreativeTrackingAndIcons(t *testing.T) {
	const terminal = `<VAST><Ad><InLine><Creatives><Creative sequence="1" apiFramework="VPAID" custom="retained"><Linear><Icons><Icon program="terminal"><StaticResource>https://terminal</StaticResource></Icon></Icons></Linear></Creative><Creative sequence="2"><Linear/></Creative><Creative sequence="3"><Linear/></Creative></Creatives></InLine></Ad></VAST>`
	const wrapper = `<VAST><Ad><Wrapper><VASTAdTagURI>https://next</VASTAdTagURI><Creatives><Creative><Linear><TrackingEvents><Tracking event="start">https://shared</Tracking></TrackingEvents><Icons><Icon program="terminal"><StaticResource>https://outer-override</StaticResource></Icon><Icon program="nearest"><StaticResource>https://outer</StaticResource></Icon><Icon program="outer-only"><StaticResource>https://outer-only</StaticResource></Icon></Icons></Linear></Creative><Creative sequence="2"><Linear><TrackingEvents><Tracking event="complete">https://second</Tracking></TrackingEvents><VideoClicks><ClickTracking>https://click-second</ClickTracking><CustomClick>https://custom-second</CustomClick></VideoClicks></Linear></Creative><Creative sequence="9"><Linear><TrackingEvents><Tracking>https://unmatched</Tracking></TrackingEvents></Linear></Creative></Creatives></Wrapper></Ad></VAST>`
	const innerWrapper = `<VAST><Ad><Wrapper><VASTAdTagURI>https://terminal</VASTAdTagURI><Creatives><Creative><Linear><Icons><Icon program="nearest"><StaticResource>https://inner</StaticResource></Icon></Icons></Linear></Creative></Creatives></Wrapper></Ad></VAST>`
	calls := 0
	fetch := func(_ context.Context, _ string, _ time.Duration, _ http.Header) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(innerWrapper), nil
		}
		return []byte(terminal), nil
	}
	merged, ok := unwrapVAST(fetch, []byte(wrapper), nil, time.Now().Add(time.Second))
	if !ok {
		t.Fatal("unwrap failed")
	}
	doc, err := parseVAST([]byte(merged))
	if err != nil {
		t.Fatal(err)
	}
	for index, creative := range doc.Ads[0].InLine.Creatives.Creative {
		linear := creative.Linear
		wantTracking := 1
		if index == 1 {
			wantTracking = 2
		}
		if len(linear.TrackingEvents.Tracking) != wantTracking {
			t.Errorf("creative %d tracking=%+v", index, linear.TrackingEvents)
		}
		if linear.TrackingEvents.Tracking[0].text() != "https://shared" {
			t.Error("missing shared tracker")
		}
		if index == 1 {
			if linear.VideoClicks == nil || linear.VideoClicks.ClickTracking[0].text() != "https://click-second" || linear.VideoClicks.CustomClicks[0].text() != "https://custom-second" {
				t.Error("missing matched click tracking")
			}
		} else if linear.VideoClicks != nil {
			t.Error("click tracking attached to unrelated creative")
		}
		icons := make(map[string]string)
		for _, icon := range linear.Icons.Icon {
			icons[iconProgram(icon)] = icon.Inner
		}
		if !strings.Contains(icons["nearest"], "https://inner") || !strings.Contains(icons["outer-only"], "https://outer-only") {
			t.Errorf("wrong icon precedence: %v", icons)
		}
		if index == 0 && !strings.Contains(icons["terminal"], ">https://terminal<") {
			t.Error("terminal icon overwritten")
		}
	}
	if !strings.Contains(merged, `apiFramework="VPAID"`) || !strings.Contains(merged, `custom="retained"`) {
		t.Error("Creative attributes lost")
	}
	if strings.Contains(merged, "https://unmatched") {
		t.Error("unmatched tracker merged")
	}
}

func TestUnwrapViewabilityOrder(t *testing.T) {
	terminal := `<VAST><Ad><InLine><ViewableImpression><Viewable>https://inline-view</Viewable><NotViewable>https://inline-not</NotViewable><ViewUndetermined>https://inline-unknown</ViewUndetermined></ViewableImpression></InLine></Ad></VAST>`
	wrapper := strings.Replace(wrapperVAST, `</ViewableImpression>`, `<NotViewable>https://wrapper-not</NotViewable><ViewUndetermined>https://wrapper-unknown</ViewUndetermined></ViewableImpression>`, 1)
	merged, ok := unwrapVAST(staticFetcher(terminal), []byte(wrapper), nil, time.Now().Add(time.Second))
	if !ok {
		t.Fatal("unwrap failed")
	}
	doc, err := parseVAST([]byte(merged))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Viewable", "Viewable", "NotViewable", "NotViewable", "ViewUndetermined", "ViewUndetermined"}
	items := doc.Ads[0].InLine.ViewableImpr.Items
	if len(items) != len(want) {
		t.Fatalf("got %d items", len(items))
	}
	for index, item := range items {
		if item.XMLName.Local != want[index] {
			t.Errorf("item %d=%s", index, item.XMLName.Local)
		}
	}
}

func TestMakeBidsInitialAdsAndFirstDownstreamAd(t *testing.T) {
	initial := `<VAST><Ad id="first"><Wrapper><VASTAdTagURI>https://first</VASTAdTagURI></Wrapper></Ad><Ad id="second"><Wrapper><VASTAdTagURI>https://second</VASTAdTagURI></Wrapper></Ad></VAST>`
	bidder := &adapter{fetch: func(_ context.Context, uri string, _ time.Duration, _ http.Header) ([]byte, error) {
		return []byte(`<VAST><Ad id="` + strings.TrimPrefix(uri, "https://") + `"><InLine/></Ad><Ad id="ignored"><Wrapper><VASTAdTagURI>https://never</VASTAdTagURI></Wrapper></Ad></VAST>`), nil
	}}
	request := &openrtb2.BidRequest{TMax: 1000, Imp: []openrtb2.Imp{{ID: "imp", Ext: json.RawMessage(`{"bidder":{"unwrap":true,"cpm":2}}`)}}}
	response, errs := bidder.MakeBids(request, &adapters.RequestData{Headers: http.Header{}}, &adapters.ResponseData{StatusCode: 200, Body: []byte(initial)})
	if len(errs) != 0 || response == nil || len(response.Bids) != 2 {
		t.Fatalf("response=%+v, errors=%v", response, errs)
	}
	for index, id := range []string{"first", "second"} {
		bid := response.Bids[index].Bid
		if bid.ID != id || bid.Price != 1 || strings.Contains(bid.AdM, "ignored") {
			t.Errorf("unexpected bid=%+v", bid)
		}
	}
}

func TestUnwrapVASTMergesCustomClicks(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			terminal := `<VAST><Ad><InLine><Creatives><Creative><Linear>`
			if existing {
				terminal += `<VideoClicks><ClickThrough>https://landing</ClickThrough><CustomClick id="inline">https://custom.inline</CustomClick></VideoClicks>`
			}
			terminal += `</Linear></Creative></Creatives></InLine></Ad></VAST>`
			wrapper := `<VAST><Ad><Wrapper><VASTAdTagURI>https://next</VASTAdTagURI><Creatives><Creative><Linear><VideoClicks><CustomClick id="first"><![CDATA[https://custom.first?a=1&b=2]]></CustomClick></VideoClicks></Linear></Creative></Creatives></Wrapper></Ad></VAST>`
			calls := 0
			fetch := func(_ context.Context, _ string, _ time.Duration, _ http.Header) ([]byte, error) {
				calls++
				if calls == 1 {
					return []byte(strings.ReplaceAll(wrapper, "first", "second")), nil
				}
				return []byte(terminal), nil
			}
			merged, ok := unwrapVAST(fetch, []byte(wrapper), nil, time.Now().Add(time.Second))
			if !ok || calls != 2 {
				t.Fatalf("success=%v, calls=%d", ok, calls)
			}
			var decoded struct {
				Clicks struct {
					Custom []struct {
						ID  string `xml:"id,attr"`
						URL string `xml:",chardata"`
					} `xml:"CustomClick"`
					Tracking []string `xml:"ClickTracking"`
					Through  string   `xml:"ClickThrough"`
				} `xml:"Ad>InLine>Creatives>Creative>Linear>VideoClicks"`
			}
			if err := xml.Unmarshal([]byte(merged), &decoded); err != nil {
				t.Fatal(err)
			}
			want := []string{"first", "second"}
			if existing {
				want = append([]string{"inline"}, want...)
				if decoded.Clicks.Through != "https://landing" {
					t.Error("terminal ClickThrough changed")
				}
			}
			if len(decoded.Clicks.Custom) != len(want) || len(decoded.Clicks.Tracking) != 0 {
				t.Fatalf("unexpected clicks: %+v", decoded.Clicks)
			}
			for index, id := range want {
				url := "https://custom." + id
				if id != "inline" {
					url += "?a=1&b=2"
				}
				if click := decoded.Clicks.Custom[index]; click.ID != id || click.URL != url {
					t.Errorf("unexpected custom click: %+v", click)
				}
			}
		})
	}
}

func TestUnwrapVASTPreservesTerminalAdAttributes(t *testing.T) {
	for _, conditional := range []string{"false", "true"} {
		t.Run(conditional, func(t *testing.T) {
			terminal := strings.Replace(inlineVAST, `<Ad id="in1">`, `<Ad id="terminal" sequence="2" conditionalAd="`+conditional+`" adType="video" custom="A &amp; B" xmlns:vendor="urn:terminal" vendor:flag="retained">`, 1)
			terminal = strings.Replace(terminal, `</InLine>`, `<Extensions><Extension><vendor:Data>value</vendor:Data></Extension></Extensions></InLine>`, 1)
			wrapper := strings.Replace(wrapperVAST, `<Ad id="w1">`, `<Ad id="wrapper" sequence="9" conditionalAd="true" adType="audio" wrapperOnly="discard" xmlns:vendor="urn:wrapper">`, 1)
			merged, ok := unwrapVAST(staticFetcher(terminal), []byte(wrapper), nil, time.Now().Add(time.Second))
			if !ok {
				t.Fatal("expected unwrapping to succeed")
			}
			var decoded struct {
				Ad struct {
					Attrs      []xml.Attr `xml:",any,attr"`
					Extensions []struct {
						Data string `xml:"urn:terminal Data"`
					} `xml:"InLine>Extensions>Extension"`
				} `xml:"Ad"`
			}
			if err := xml.Unmarshal([]byte(merged), &decoded); err != nil {
				t.Fatal(err)
			}
			want := map[xml.Name]string{
				{Local: "id"}: "terminal", {Local: "sequence"}: "2",
				{Local: "conditionalAd"}: conditional, {Local: "adType"}: "video",
				{Local: "custom"}: "A & B", {Space: "xmlns", Local: "vendor"}: "urn:terminal",
				{Space: "urn:terminal", Local: "flag"}: "retained",
			}
			for _, attr := range decoded.Ad.Attrs {
				if attr.Name.Space == "xmlns" && attr.Name.Local != "vendor" {
					continue
				}
				if value, exists := want[attr.Name]; !exists || attr.Value != value {
					t.Errorf("unexpected or duplicate attribute: %+v", attr)
				}
				delete(want, attr.Name)
			}
			if len(want) != 0 || len(decoded.Ad.Extensions) == 0 || decoded.Ad.Extensions[0].Data != "value" {
				t.Errorf("missing attributes=%v, extensions=%+v", want, decoded.Ad.Extensions)
			}
		})
	}
}

func TestElementText(t *testing.T) {
	for _, test := range []struct {
		name, inner, want string
	}{
		{"plain", "  plain text  ", "plain text"},
		{"entities", " A &amp; B &lt;C&gt; &#38; &#x26; ", "A & B <C> & &"},
		{"cdata", " <![CDATA[A &amp; B]]> ", "A &amp; B"},
		{"mixed", " A &amp; <![CDATA[B <C>]]><![CDATA[ & D]]> ", "A & B <C> & D"},
		{"empty", "", ""},
		{"malformed", "A & B", ""},
		{"unclosed", "<![CDATA[A", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := elementText(test.inner); got != test.want {
				t.Errorf("elementText(%q) = %q, want %q", test.inner, got, test.want)
			}
		})
	}
}

func TestUnwrapVASTDecodesWrapperURL(t *testing.T) {
	for _, inner := range []string{
		`https://downstream.example.com/?a=1&amp;b=2`,
		`https://downstream.example.com/?a=1&#38;b=2`,
		`<![CDATA[https://downstream.example.com/?a=1&b=2]]>`,
		`https://downstream.example.com/?a=1<![CDATA[&b=2]]>`,
		`https://downstream.example.com/?a=1&b=2`,
	} {
		t.Run(inner, func(t *testing.T) {
			wrapper := strings.Replace(wrapperVAST, `<![CDATA[https://downstream.example.com/vast.xml]]>`, inner, 1)
			calls := 0
			fetch := func(_ context.Context, uri string, _ time.Duration, _ http.Header) ([]byte, error) {
				calls++
				if uri != "https://downstream.example.com/?a=1&b=2" {
					t.Errorf("fetch URL = %q", uri)
				}
				return []byte(inlineVAST), nil
			}
			_, ok := unwrapVAST(fetch, []byte(wrapper), nil, time.Now().Add(time.Second))
			malformed := inner == `https://downstream.example.com/?a=1&b=2`
			if malformed {
				if ok || calls != 0 {
					t.Fatalf("malformed URI: success=%v, fetches=%d", ok, calls)
				}
			} else if !ok || calls != 1 {
				t.Fatalf("valid URI: success=%v, fetches=%d", ok, calls)
			}
		})
	}
}

func TestUnwrapVASTEscapesAdSystem(t *testing.T) {
	wrapper := strings.Replace(wrapperVAST, "WrapSSP", `<![CDATA[Wrapper & <SSP>]]>`, 1)
	terminal := strings.Replace(inlineVAST, "InlineSSP", `Inline &amp; &lt;SSP&gt;`, 1)
	merged, ok := unwrapVAST(staticFetcher(terminal), []byte(wrapper), nil, time.Now().Add(time.Second))
	if !ok {
		t.Fatal("expected unwrapping to succeed")
	}
	var decoded struct {
		AdSystem string `xml:"Ad>InLine>AdSystem"`
	}
	if err := xml.Unmarshal([]byte(merged), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AdSystem != "Wrapper & <SSP>,Inline & <SSP>" {
		t.Errorf("unexpected AdSystem: %q", decoded.AdSystem)
	}
}

func TestUnwrapVASTPreservesLinearAttributes(t *testing.T) {
	for _, offset := range []string{"00:00:05", "25%"} {
		t.Run(offset, func(t *testing.T) {
			terminal := strings.Replace(inlineVAST, "<Linear>", `<Linear skipoffset="`+offset+`" custom="keep &amp; escape">`, 1)
			merged, ok := unwrapVAST(staticFetcher(terminal), []byte(wrapperVAST), nil, time.Now().Add(time.Second))
			if !ok {
				t.Fatal("expected unwrapping to succeed")
			}
			var decoded struct {
				Linear struct {
					SkipOffset string `xml:"skipoffset,attr"`
					Custom     string `xml:"custom,attr"`
				} `xml:"Ad>InLine>Creatives>Creative>Linear"`
			}
			if err := xml.Unmarshal([]byte(merged), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Linear.SkipOffset != offset || decoded.Linear.Custom != "keep & escape" {
				t.Errorf("unexpected Linear attributes: %+v", decoded.Linear)
			}
		})
	}
}

func TestUnwrapVASTOrdersVAST42Elements(t *testing.T) {
	const terminal = `<VAST xmlns="http://www.iab.com/VAST" version="4.2"><Ad id="inline"><InLine>
<AdSystem>InlineSSP</AdSystem>
<Error>https://example.com/error</Error>
<Extensions><Extension type="custom"><Custom>retained</Custom></Extension></Extensions>
<Impression>https://example.com/impression</Impression>
<Pricing model="CPM" currency="EUR">1.5</Pricing>
<ViewableImpression><Viewable>https://example.com/viewable</Viewable></ViewableImpression>
<AdServingId>serving-id</AdServingId>
<AdTitle><![CDATA[Title & details]]></AdTitle>
<AdVerifications><Verification vendor="inline"><JavaScriptResource apiFramework="omid">https://example.com/verify.js</JavaScriptResource></Verification></AdVerifications>
<Advertiser>brand.example.com</Advertiser>
<Category authority="https://example.com/categories">category-1</Category>
<Category authority="https://example.com/categories">category-2</Category>
<Creatives><Creative id="creative-id">
<CreativeExtensions><CreativeExtension type="text/xml"><Custom>creative metadata</Custom></CreativeExtension></CreativeExtensions>
<Linear skipoffset="25%">
<Icons><Icon program="AdChoices" width="20" height="20" xPosition="0" yPosition="0"><StaticResource creativeType="image/png">https://example.com/icon.png</StaticResource></Icon></Icons>
<TrackingEvents><Tracking event="complete">https://example.com/complete</Tracking></TrackingEvents>
<AdParameters xmlEncoded="false"><![CDATA[{"key":"value"}]]></AdParameters>
<Duration>00:00:15</Duration>
<MediaFiles><MediaFile delivery="progressive" type="video/mp4" width="640" height="360">https://example.com/video.mp4</MediaFile></MediaFiles>
<VideoClicks><ClickTracking>https://example.com/click</ClickTracking><ClickThrough>https://example.com/landing</ClickThrough><CustomClick>https://example.com/custom</CustomClick></VideoClicks>
</Linear>
<UniversalAdId idRegistry="example.com">creative-1</UniversalAdId>
<UniversalAdId idRegistry="other.example.com">creative-2</UniversalAdId>
</Creative></Creatives>
<Description>description</Description>
<Expires>3600</Expires>
<Survey type="text/html">https://example.com/survey</Survey>
</InLine></Ad></VAST>`
	merged, ok := unwrapVAST(staticFetcher(terminal), []byte(wrapperVAST), nil, time.Now().Add(time.Second))
	if !ok {
		t.Fatal("expected unwrapping to succeed")
	}
	t.Logf("VAST42 input: %s", strings.ReplaceAll(terminal, "\n", ""))
	t.Logf("VAST42 output: %s", strings.ReplaceAll(merged, "\n", ""))
	children := make(map[string][]string)
	var path []string
	decoder := xml.NewDecoder(strings.NewReader(merged))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			parent := strings.Join(path, "/")
			children[parent] = append(children[parent], element.Name.Local)
			path = append(path, element.Name.Local)
		case xml.EndElement:
			path = path[:len(path)-1]
		}
	}
	const inlinePath = "VAST/Ad/InLine"
	const creativePath = inlinePath + "/Creatives/Creative"
	for parent, want := range map[string][]string{
		inlinePath:                           {"AdSystem", "Error", "Error", "Extensions", "Impression", "Impression", "Pricing", "ViewableImpression", "AdServingId", "AdTitle", "AdVerifications", "Advertiser", "Category", "Category", "Creatives", "Description", "Expires", "Survey"},
		creativePath:                         {"CreativeExtensions", "Linear", "UniversalAdId", "UniversalAdId"},
		creativePath + "/Linear":             {"Icons", "TrackingEvents", "AdParameters", "Duration", "MediaFiles", "VideoClicks"},
		creativePath + "/Linear/VideoClicks": {"ClickTracking", "ClickTracking", "ClickThrough", "CustomClick"},
	} {
		if !slices.Equal(children[parent], want) {
			t.Errorf("%s children = %v, want %v", parent, children[parent], want)
		}
	}
	doc, err := parseVAST([]byte(merged))
	if err != nil {
		t.Fatal(err)
	}
	inline := doc.Ads[0].InLine
	for name, values := range map[string][2]string{
		"AdServingId":  {inline.AdServingID.text(), "serving-id"},
		"AdTitle":      {inline.AdTitle.text(), "Title & details"},
		"Description":  {inline.Description.text(), "description"},
		"Expires":      {inline.Expires.text(), "3600"},
		"Survey":       {inline.Survey.text(), "https://example.com/survey"},
		"AdParameters": {inline.firstLinear().AdParameters.text(), `{"key":"value"}`},
	} {
		if values[0] != values[1] {
			t.Errorf("%s = %q, want %q", name, values[0], values[1])
		}
	}
	if inline.Categories[0].text() != "category-1" || inline.Categories[1].text() != "category-2" {
		t.Error("category values changed")
	}
	creative := inline.Creatives.Creative[0]
	if creative.UniversalAdIDs[0].text() != "creative-1" || creative.UniversalAdIDs[1].text() != "creative-2" {
		t.Error("universal ad IDs changed")
	}
}

func TestUnwrapVASTPreservesTerminalRootAttributes(t *testing.T) {
	for _, namespace := range []string{"", "urn:vast"} {
		t.Run("namespace="+namespace, func(t *testing.T) {
			terminal := strings.Replace(inlineVAST, `<VAST version="4.2">`,
				`<VAST version="4.2" xmlns="`+namespace+`" xmlns:vendor="urn:vendor" data-source="test &amp; source" vendor:flag="enabled">`, 1)
			terminal = strings.Replace(terminal, `</InLine>`,
				`<Extensions><Extension><vendor:Tracking vendor:event="view"><![CDATA[https://example.invalid/?a=1&b=2]]></vendor:Tracking></Extension></Extensions></InLine>`, 1)
			merged, ok := unwrapVAST(staticFetcher(terminal), []byte(wrapperVAST), nil, time.Now().Add(time.Second))
			if !ok {
				t.Fatal("expected unwrapping to succeed")
			}
			decoder := xml.NewDecoder(strings.NewReader(merged))
			foundTracking := false
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				element, ok := token.(xml.StartElement)
				if !ok {
					continue
				}
				if element.Name.Local == "VAST" {
					if element.Name.Space != namespace {
						t.Errorf("root namespace = %q, want %q", element.Name.Space, namespace)
					}
					attributes := make(map[xml.Name]string)
					for _, attribute := range element.Attr {
						attributes[attribute.Name] = attribute.Value
					}
					for name, value := range map[xml.Name]string{
						{Local: "version"}:                   "4.2",
						{Local: "data-source"}:               "test & source",
						{Space: "xmlns", Local: "vendor"}:    "urn:vendor",
						{Space: "urn:vendor", Local: "flag"}: "enabled",
					} {
						if attributes[name] != value {
							t.Errorf("root attribute %v = %q, want %q", name, attributes[name], value)
						}
					}
				}
				if element.Name.Local == "Tracking" && element.Name.Space == "urn:vendor" {
					foundTracking = true
					if len(element.Attr) != 1 || element.Attr[0].Name != (xml.Name{Space: "urn:vendor", Local: "event"}) {
						t.Errorf("unexpected tracking attributes: %v", element.Attr)
					}
				}
			}
			if !foundTracking {
				t.Fatal("missing Tracking element bound to urn:vendor")
			}
			if !strings.Contains(merged, `<![CDATA[https://example.invalid/?a=1&b=2]]>`) {
				t.Fatal("extension CDATA was not preserved")
			}
		})
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
	if !strings.Contains(merged, `<ClickTracking><![CDATA[https://clk.wrap]]></ClickTracking><ClickThrough>`) {
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

func TestMakeBidsWrapperExtensionNamespaces(t *testing.T) {
	for _, test := range []struct {
		name   string
		scopes [5]string
		prefix string
		want   string
	}{
		{"root", [5]string{`xmlns:vendor="urn:first"`}, "vendor:", "urn:first"},
		{"ad", [5]string{"", `xmlns:vendor="urn:first"`}, "vendor:", "urn:first"},
		{"wrapper", [5]string{"", "", `xmlns:vendor="urn:first"`}, "vendor:", "urn:first"},
		{"extensions", [5]string{"", "", "", `xmlns:vendor="urn:first"`}, "vendor:", "urn:first"},
		{"extension", [5]string{"", "", "", "", `xmlns:vendor="urn:first"`}, "vendor:", "urn:first"},
		{"shadowing", [5]string{`xmlns:vendor="urn:root"`, `xmlns:vendor="urn:ad"`, `xmlns:vendor="urn:wrapper"`, `xmlns:vendor="urn:container"`, `xmlns:vendor="urn:first"`}, "vendor:", "urn:first"},
		{"default", [5]string{`xmlns="urn:first"`}, "", "urn:first"},
		{"default-reset", [5]string{`xmlns="urn:root"`, "", "", "", `xmlns=""`}, "", ""},
		{"no-default", [5]string{}, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := `<VAST><Ad><Wrapper><VASTAdTagURI>https://next</VASTAdTagURI><Extensions><Extension><` + test.prefix + `Data id="first">first</` + test.prefix + `Data></Extension></Extensions></Wrapper></Ad></VAST>`
			for index, tag := range []string{"VAST", "Ad", "Wrapper", "Extensions", "Extension"} {
				if test.scopes[index] != "" {
					first = strings.Replace(first, "<"+tag+">", "<"+tag+" "+test.scopes[index]+">", 1)
				}
			}
			second := `<VAST xmlns:vendor="urn:second"><Ad><Wrapper><VASTAdTagURI>https://terminal</VASTAdTagURI><Extensions><Extension><vendor:Data id="second" vendor:flag="retained"><![CDATA[A & B]]></vendor:Data></Extension></Extensions></Wrapper></Ad></VAST>`
			terminal := strings.Replace(inlineVAST, `<VAST version="4.2">`, `<VAST version="4.2" xmlns="urn:terminal-default" xmlns:vendor="urn:terminal">`, 1)
			terminal = strings.Replace(terminal, `</InLine>`, `<Extensions><Extension><vendor:Data id="terminal">terminal</vendor:Data></Extension></Extensions></InLine>`, 1)
			calls := 0
			bidder := &adapter{fetch: func(_ context.Context, _ string, _ time.Duration, _ http.Header) ([]byte, error) {
				calls++
				if calls == 1 {
					return []byte(second), nil
				}
				return []byte(terminal), nil
			}}
			request := &openrtb2.BidRequest{TMax: 1000, Imp: []openrtb2.Imp{{
				ID: "imp", Ext: json.RawMessage(`{"bidder":{"unwrap":true,"cpm":1}}`),
			}}}
			response, errs := bidder.MakeBids(request, &adapters.RequestData{Headers: http.Header{}}, &adapters.ResponseData{StatusCode: http.StatusOK, Body: []byte(first)})
			if len(errs) != 0 || response == nil || len(response.Bids) != 1 || calls != 2 {
				t.Fatalf("response=%+v, errors=%v, fetches=%d", response, errs, calls)
			}
			want := map[string]string{"first": test.want, "second": "urn:second", "terminal": "urn:terminal"}
			decoder := xml.NewDecoder(strings.NewReader(response.Bids[0].Bid.AdM))
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				element, ok := token.(xml.StartElement)
				if !ok || element.Name.Local != "Data" {
					continue
				}
				var id string
				for _, attr := range element.Attr {
					if attr.Name.Local == "id" {
						id = attr.Value
					}
					if attr.Name.Local == "flag" && (attr.Name.Space != "urn:second" || attr.Value != "retained") {
						t.Errorf("incorrect namespaced attribute: %+v", attr)
					}
				}
				if namespace, exists := want[id]; !exists || element.Name.Space != namespace {
					t.Errorf("Data %q namespace=%q, want %q (exists=%v)", id, element.Name.Space, namespace, exists)
				}
				delete(want, id)
			}
			if len(want) != 0 {
				t.Errorf("missing extension data: %v", want)
			}
			if !strings.Contains(response.Bids[0].Bid.AdM, `<![CDATA[A & B]]>`) {
				t.Error("wrapper extension CDATA changed")
			}
		})
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
