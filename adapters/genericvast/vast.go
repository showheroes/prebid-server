package genericvast

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// vastDoc is the minimal VAST document we decode. Unknown elements are tolerated.
type vastDoc struct {
	XMLName xml.Name `xml:"VAST"`
	Version string   `xml:"version,attr"`
	Ads     []vastAd `xml:"Ad"`
}

// vastAd captures the <Ad> element. InnerXML is preserved so that we can
// re-emit the original ad (including unknown extensions) verbatim per bid.
type vastAd struct {
	ID       string       `xml:"id,attr,omitempty"`
	Sequence string       `xml:"sequence,attr,omitempty"`
	InnerXML string       `xml:",innerxml"`
	InLine   *vastInLine  `xml:"InLine,omitempty"`
	Wrapper  *vastWrapper `xml:"Wrapper,omitempty"`
}

type vastInLine struct {
	AdSystem   string        `xml:"AdSystem"`
	Advertiser string        `xml:"Advertiser"`
	Pricing    *vastPricing  `xml:"Pricing,omitempty"`
	Creatives  vastCreatives `xml:"Creatives"`
}

type vastWrapper struct {
	AdSystem   string        `xml:"AdSystem"`
	Advertiser string        `xml:"Advertiser"`
	Pricing    *vastPricing  `xml:"Pricing,omitempty"`
	Creatives  vastCreatives `xml:"Creatives"`
}

type vastPricing struct {
	Currency string `xml:"currency,attr"`
	Model    string `xml:"model,attr"`
	Value    string `xml:",chardata"`
}

type vastCreatives struct {
	Creative []vastCreative `xml:"Creative"`
}

type vastCreative struct {
	ID     string      `xml:"id,attr"`
	Linear *vastLinear `xml:"Linear,omitempty"`
}

type vastLinear struct {
	Duration string `xml:"Duration"`
}

// parseVAST decodes the VAST document. Leading whitespace, BOM, and XML
// declaration are accepted; unknown elements are ignored.
func parseVAST(body []byte) (*vastDoc, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	var doc vastDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// advertiserValue returns the <Advertiser> text from an InLine or Wrapper, whichever is set.
func (a *vastAd) advertiserValue() string {
	switch {
	case a.InLine != nil:
		return strings.TrimSpace(a.InLine.Advertiser)
	case a.Wrapper != nil:
		return strings.TrimSpace(a.Wrapper.Advertiser)
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
	if a.InLine != nil {
		return a.InLine.Creatives.Creative
	}
	if a.Wrapper != nil {
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
		d := strings.TrimSpace(c.Linear.Duration)
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
