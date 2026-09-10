package genericvast

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// vastDoc is the decoded VAST document. Elements we don't manipulate are preserved
// verbatim (attributes, CDATA, and unknown descendants) so a decode/encode round-trip
// stays loss-tolerant.
type vastDoc struct {
	XMLName xml.Name   `xml:"VAST"`
	Version string     `xml:"version,attr"`
	Attrs   []xml.Attr `xml:",any,attr"`
	Ads     []vastAd   `xml:"Ad"`
}

// vastAd captures the <Ad> element. InnerXML is retained so a wrapper/inline ad can be
// re-emitted verbatim (non-unwrap path) without re-encoding.
type vastAd struct {
	ID       string       `xml:"id,attr,omitempty"`
	Sequence string       `xml:"sequence,attr,omitempty"`
	Attrs    []xml.Attr   `xml:",any,attr"`
	InnerXML string       `xml:",innerxml"`
	InLine   *vastInLine  `xml:"InLine,omitempty"`
	Wrapper  *vastWrapper `xml:"Wrapper,omitempty"`
}

// rawEl preserves an element's attributes and inner XML verbatim (CDATA-safe). The
// element name comes from the containing field's tag.
type rawEl struct {
	Attrs []xml.Attr `xml:",any,attr"`
	Inner string     `xml:",innerxml"`
}

// text returns the element's decoded character data.
func (r *rawEl) text() string {
	if r == nil {
		return ""
	}
	return elementText(r.Inner)
}

// anyEl also preserves the element's own name, for pass-through of elements we don't
// model explicitly.
type anyEl struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Inner   string     `xml:",innerxml"`
}

type vastInLine struct {
	AdSystem        *rawEl              `xml:"AdSystem,omitempty"`
	Errors          []rawEl             `xml:"Error"`
	Extensions      *extensions         `xml:"Extensions,omitempty"`
	Impressions     []rawEl             `xml:"Impression"`
	Pricing         *vastPricing        `xml:"Pricing,omitempty"`
	ViewableImpr    *viewableImpression `xml:"ViewableImpression,omitempty"`
	AdServingID     *rawEl              `xml:"AdServingId,omitempty"`
	AdTitle         *rawEl              `xml:"AdTitle,omitempty"`
	AdVerifications *adVerifications    `xml:"AdVerifications,omitempty"`
	Advertiser      *rawEl              `xml:"Advertiser,omitempty"`
	Categories      []rawEl             `xml:"Category"`
	Creatives       *vastCreatives      `xml:"Creatives,omitempty"`
	Description     *rawEl              `xml:"Description,omitempty"`
	Expires         *rawEl              `xml:"Expires,omitempty"`
	Survey          *rawEl              `xml:"Survey,omitempty"`
	Other           []anyEl             `xml:",any"`
}

type vastWrapper struct {
	AdSystem        *rawEl              `xml:"AdSystem,omitempty"`
	Advertiser      *rawEl              `xml:"Advertiser,omitempty"`
	VASTAdTagURI    *rawEl              `xml:"VASTAdTagURI,omitempty"`
	Impressions     []rawEl             `xml:"Impression"`
	Pricing         *vastPricing        `xml:"Pricing,omitempty"`
	Errors          []rawEl             `xml:"Error"`
	ViewableImpr    *viewableImpression `xml:"ViewableImpression,omitempty"`
	AdVerifications *adVerifications    `xml:"AdVerifications,omitempty"`
	Creatives       *vastCreatives      `xml:"Creatives,omitempty"`
	Extensions      *extensions         `xml:"Extensions,omitempty"`
	Other           []anyEl             `xml:",any"`
}

type vastPricing struct {
	Currency string `xml:"currency,attr,omitempty"`
	Model    string `xml:"model,attr,omitempty"`
	Value    string `xml:",chardata"`
}

type viewableImpression struct {
	ID    string  `xml:"id,attr,omitempty"`
	Items []anyEl `xml:",any"`
}

type adVerifications struct {
	Verification []rawEl `xml:"Verification"`
	Other        []anyEl `xml:",any"`
}

type extensions struct {
	Extension []rawEl `xml:"Extension"`
	Other     []anyEl `xml:",any"`
}

type vastCreatives struct {
	Creative []vastCreative `xml:"Creative"`
}

type vastCreative struct {
	ID                 string      `xml:"id,attr,omitempty"`
	AdID               string      `xml:"adId,attr,omitempty"`
	Sequence           string      `xml:"sequence,attr,omitempty"`
	CreativeExtensions *rawEl      `xml:"CreativeExtensions,omitempty"`
	Linear             *vastLinear `xml:"Linear,omitempty"`
	UniversalAdIDs     []rawEl     `xml:"UniversalAdId"`
	Other              []anyEl     `xml:",any"`
}

type vastLinear struct {
	Attrs          []xml.Attr      `xml:",any,attr"`
	Icons          *rawEl          `xml:"Icons,omitempty"`
	TrackingEvents *trackingEvents `xml:"TrackingEvents,omitempty"`
	AdParameters   *rawEl          `xml:"AdParameters,omitempty"`
	Duration       *rawEl          `xml:"Duration,omitempty"`
	MediaFiles     *rawEl          `xml:"MediaFiles,omitempty"`
	VideoClicks    *videoClicks    `xml:"VideoClicks,omitempty"`
	Other          []anyEl         `xml:",any"`
}

type trackingEvents struct {
	Tracking []rawEl `xml:"Tracking"`
	Other    []anyEl `xml:",any"`
}

type videoClicks struct {
	ClickTracking []rawEl `xml:"ClickTracking"`
	ClickThrough  *rawEl  `xml:"ClickThrough,omitempty"`
	CustomClicks  []rawEl `xml:"CustomClick"`
	Other         []anyEl `xml:",any"`
}

// parseVAST decodes the VAST document. Leading whitespace, BOM, and XML
// declaration are accepted; unknown elements are tolerated.
func parseVAST(body []byte) (*vastDoc, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	var doc vastDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// elementText decodes XML character data and trims surrounding whitespace.
func elementText(inner string) string {
	var value struct {
		Text string `xml:",chardata"`
	}
	if err := xml.Unmarshal([]byte("<value>"+inner+"</value>"), &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value.Text)
}

// advertiserValue returns the <Advertiser> text from an InLine or Wrapper, whichever is set.
func (a *vastAd) advertiserValue() string {
	switch {
	case a.InLine != nil:
		return a.InLine.Advertiser.text()
	case a.Wrapper != nil:
		return a.Wrapper.Advertiser.text()
	}
	return ""
}

// pricing returns the first non-empty <Pricing> from InLine/Wrapper.
func (a *vastAd) pricing() *vastPricing {
	if a.InLine != nil && a.InLine.Pricing != nil {
		return a.InLine.Pricing
	}
	if a.Wrapper != nil && a.Wrapper.Pricing != nil {
		return a.Wrapper.Pricing
	}
	return nil
}

// creatives returns InLine or Wrapper creatives.
func (a *vastAd) creatives() []vastCreative {
	switch {
	case a.InLine != nil && a.InLine.Creatives != nil:
		return a.InLine.Creatives.Creative
	case a.Wrapper != nil && a.Wrapper.Creatives != nil:
		return a.Wrapper.Creatives.Creative
	}
	return nil
}

// extractPrice returns (price, currency, ok). Only <Pricing model="CPM"> is honored;
// other models (CPC/CPE/CPV) are ignored so the caller can fall back to the configured
// CPM. An empty model attribute is treated as CPM for backward compatibility with
// VAST 3 responses that omit the attribute.
func extractPrice(ad *vastAd) (float64, string, bool) {
	p := ad.pricing()
	if p == nil {
		return 0, "", false
	}
	model := strings.TrimSpace(p.Model)
	if model != "" && !strings.EqualFold(model, "CPM") {
		return 0, "", false
	}
	value := strings.TrimSpace(p.Value)
	if value == "" {
		return 0, p.Currency, false
	}
	price, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, p.Currency, false
	}
	return price, p.Currency, true
}

// extractCreativeID returns the first Creative@id, or empty.
func extractCreativeID(ad *vastAd) string {
	for _, c := range ad.creatives() {
		if c.ID != "" {
			return c.ID
		}
	}
	return ""
}

// extractDurationSeconds parses Linear/Duration in HH:MM:SS[.fff] and returns
// rounded seconds. Returns 0 if absent or unparsable.
func extractDurationSeconds(ad *vastAd) int64 {
	for _, c := range ad.creatives() {
		if c.Linear == nil {
			continue
		}
		d := c.Linear.Duration.text()
		if d == "" {
			continue
		}
		if secs, ok := parseDurationHMS(d); ok {
			return secs
		}
	}
	return 0
}

func parseDurationHMS(s string) (int64, bool) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return 0, false
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 {
		return 0, false
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, false
	}
	secStr := parts[2]
	// allow fractional seconds
	secFloat, err := strconv.ParseFloat(secStr, 64)
	if err != nil || secFloat < 0 {
		return 0, false
	}
	total := int64(h)*3600 + int64(m)*60 + int64(secFloat+0.5)
	return total, true
}

// extractAdDomains returns []string{advertiser} when the VAST <Advertiser> field
// looks like a domain (contains a dot and no whitespace). Otherwise returns nil.
// Per IAB VAST 4, <Advertiser> carries the advertiser's identity (commonly a domain),
// while <AdSystem> identifies the ad-serving system and is not appropriate for ADomain.
func extractAdDomains(ad *vastAd) []string {
	s := ad.advertiserValue()
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return nil
	}
	if !strings.Contains(s, ".") {
		return nil
	}
	return []string{s}
}

// reemitAd builds a minimal `<VAST version="X"><Ad ...>{inner}</Ad></VAST>`
// envelope around a single ad, preserving its raw inner XML.
func reemitAd(version string, ad *vastAd) string {
	if version == "" {
		version = "4.0"
	}
	var attrs strings.Builder
	if ad.ID != "" {
		fmt.Fprintf(&attrs, ` id="%s"`, xmlAttrEscape(ad.ID))
	}
	if ad.Sequence != "" {
		fmt.Fprintf(&attrs, ` sequence="%s"`, xmlAttrEscape(ad.Sequence))
	}
	return fmt.Sprintf(`<VAST version="%s"><Ad%s>%s</Ad></VAST>`,
		xmlAttrEscape(version), attrs.String(), ad.InnerXML)
}

// xmlAttrEscape escapes characters that would break an XML double-quoted attribute.
func xmlAttrEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
