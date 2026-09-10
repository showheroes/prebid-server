package genericvast

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
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
	Namespaces []xml.Attr `xml:"-"`
	Attrs      []xml.Attr `xml:",any,attr"`
	Inner      string     `xml:",innerxml"`
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
	Namespaces []xml.Attr `xml:"-"`
	XMLName    xml.Name
	Attrs      []xml.Attr `xml:",any,attr"`
	Inner      string     `xml:",innerxml"`
}

type vastInLine struct {
	Attrs           []xml.Attr          `xml:",any,attr"`
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
	Attrs           []xml.Attr          `xml:",any,attr"`
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
	Attrs []xml.Attr `xml:",any,attr"`
	ID    string     `xml:"id,attr,omitempty"`
	Items []anyEl    `xml:",any"`
}

type adVerifications struct {
	Attrs        []xml.Attr `xml:",any,attr"`
	Verification []rawEl    `xml:"Verification"`
	Other        []anyEl    `xml:",any"`
}

type extensions struct {
	Attrs     []xml.Attr `xml:",any,attr"`
	Extension []rawEl    `xml:"Extension"`
	Other     []anyEl    `xml:",any"`
}

type vastCreatives struct {
	Attrs    []xml.Attr     `xml:",any,attr"`
	Creative []vastCreative `xml:"Creative"`
}

type vastCreative struct {
	Attrs              []xml.Attr  `xml:",any,attr"`
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
	Icons          *vastIcons      `xml:"Icons,omitempty"`
	TrackingEvents *trackingEvents `xml:"TrackingEvents,omitempty"`
	AdParameters   *rawEl          `xml:"AdParameters,omitempty"`
	Duration       *rawEl          `xml:"Duration,omitempty"`
	MediaFiles     *rawEl          `xml:"MediaFiles,omitempty"`
	VideoClicks    *videoClicks    `xml:"VideoClicks,omitempty"`
	Other          []anyEl         `xml:",any"`
}

type vastIcons struct {
	Attrs []xml.Attr `xml:",any,attr"`
	Icon  []rawEl    `xml:"Icon"`
}

type trackingEvents struct {
	Attrs    []xml.Attr `xml:",any,attr"`
	Tracking []rawEl    `xml:"Tracking"`
	Other    []anyEl    `xml:",any"`
}

type videoClicks struct {
	Attrs         []xml.Attr `xml:",any,attr"`
	ClickTracking []rawEl    `xml:"ClickTracking"`
	ClickThrough  *rawEl     `xml:"ClickThrough,omitempty"`
	CustomClicks  []rawEl    `xml:"CustomClick"`
	Other         []anyEl    `xml:",any"`
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
	walker := namespaceWalker{}
	scope := namespaceBindings(doc.Attrs)
	for index := range doc.Ads {
		walker.ad(&doc.Ads[index], scope)
	}
	return &doc, nil
}

func namespaceBindings(scopes ...[]xml.Attr) []xml.Attr {
	bindings := []xml.Attr{{Name: xml.Name{Local: "xmlns"}, Value: ""}}
	positions := map[xml.Name]int{bindings[0].Name: 0}
	for _, scope := range scopes {
		for _, attr := range scope {
			if attr.Name.Space != "xmlns" && attr.Name != (xml.Name{Local: "xmlns"}) {
				continue
			}
			if index, exists := positions[attr.Name]; exists {
				bindings[index] = attr
			} else {
				positions[attr.Name] = len(bindings)
				bindings = append(bindings, attr)
			}
		}
	}
	return bindings
}

type namespaceWalker struct {
	output bool
	target string
}

func (walker namespaceWalker) container(attrs *[]xml.Attr, inherited []xml.Attr) []xml.Attr {
	if walker.output {
		*attrs = marshalAttrs(*attrs)
		return nil
	}
	return namespaceBindings(inherited, *attrs)
}

func (walker namespaceWalker) leaf(attrs, namespaces *[]xml.Attr, inner *string, inherited []xml.Attr, parent string) {
	if !walker.output {
		*namespaces = namespaceBindings(inherited, *attrs)
		return
	}
	combined := make([]xml.Attr, 0, len(*attrs)+len(*namespaces))
	for _, attr := range *attrs {
		if attr.Name.Space != "xmlns" && attr.Name.Local != "xmlns" {
			combined = append(combined, attr)
		}
	}
	for _, binding := range *namespaces {
		if binding.Name.Space == "xmlns" {
			combined = append(combined, binding)
		}
		if binding.Name == (xml.Name{Local: "xmlns"}) && binding.Value != walker.target {
			*inner = bindChildNamespace(*inner, binding.Value, parent)
		}
	}
	*attrs = marshalAttrs(combined)
}

func (walker namespaceWalker) raw(element *rawEl, scope []xml.Attr, parent string) {
	if element != nil {
		walker.leaf(&element.Attrs, &element.Namespaces, &element.Inner, scope, parent)
	}
}

func (walker namespaceWalker) rawElements(elements []rawEl, scope []xml.Attr, parent string) {
	for index := range elements {
		walker.raw(&elements[index], scope, parent)
	}
}

func (walker namespaceWalker) anyElements(elements []anyEl, scope []xml.Attr) {
	for index := range elements {
		element := &elements[index]
		walker.leaf(&element.Attrs, &element.Namespaces, &element.Inner, scope, "")
	}
}

func (walker namespaceWalker) ad(ad *vastAd, inherited []xml.Attr) {
	scope := walker.container(&ad.Attrs, inherited)
	walker.inline(ad.InLine, scope)
	walker.wrapper(ad.Wrapper, scope)
}

func (walker namespaceWalker) inline(inline *vastInLine, inherited []xml.Attr) {
	if inline == nil {
		return
	}
	scope := walker.container(&inline.Attrs, inherited)
	walker.raw(inline.AdSystem, scope, "AdSystem")
	walker.rawElements(inline.Errors, scope, "Error")
	walker.extensions(inline.Extensions, scope)
	walker.rawElements(inline.Impressions, scope, "Impression")
	walker.viewable(inline.ViewableImpr, scope)
	walker.raw(inline.AdServingID, scope, "AdServingId")
	walker.raw(inline.AdTitle, scope, "AdTitle")
	walker.verifications(inline.AdVerifications, scope)
	walker.raw(inline.Advertiser, scope, "Advertiser")
	walker.rawElements(inline.Categories, scope, "Category")
	walker.creatives(inline.Creatives, scope)
	walker.raw(inline.Description, scope, "Description")
	walker.raw(inline.Expires, scope, "Expires")
	walker.raw(inline.Survey, scope, "Survey")
	walker.anyElements(inline.Other, scope)
}

func (walker namespaceWalker) wrapper(wrapper *vastWrapper, inherited []xml.Attr) {
	if wrapper == nil {
		return
	}
	scope := walker.container(&wrapper.Attrs, inherited)
	walker.raw(wrapper.AdSystem, scope, "AdSystem")
	walker.raw(wrapper.Advertiser, scope, "Advertiser")
	walker.raw(wrapper.VASTAdTagURI, scope, "VASTAdTagURI")
	walker.rawElements(wrapper.Impressions, scope, "Impression")
	walker.rawElements(wrapper.Errors, scope, "Error")
	walker.viewable(wrapper.ViewableImpr, scope)
	walker.verifications(wrapper.AdVerifications, scope)
	walker.creatives(wrapper.Creatives, scope)
	walker.extensions(wrapper.Extensions, scope)
	walker.anyElements(wrapper.Other, scope)
}

func (walker namespaceWalker) viewable(viewable *viewableImpression, inherited []xml.Attr) {
	if viewable != nil {
		scope := walker.container(&viewable.Attrs, inherited)
		walker.anyElements(viewable.Items, scope)
	}
}

func (walker namespaceWalker) verifications(verifications *adVerifications, inherited []xml.Attr) {
	if verifications != nil {
		scope := walker.container(&verifications.Attrs, inherited)
		walker.rawElements(verifications.Verification, scope, "Verification")
		walker.anyElements(verifications.Other, scope)
	}
}

func (walker namespaceWalker) extensions(extensions *extensions, inherited []xml.Attr) {
	if extensions != nil {
		scope := walker.container(&extensions.Attrs, inherited)
		walker.rawElements(extensions.Extension, scope, "Extension")
		walker.anyElements(extensions.Other, scope)
	}
}

func (walker namespaceWalker) creatives(creatives *vastCreatives, inherited []xml.Attr) {
	if creatives == nil {
		return
	}
	scope := walker.container(&creatives.Attrs, inherited)
	for index := range creatives.Creative {
		creative := &creatives.Creative[index]
		creativeScope := walker.container(&creative.Attrs, scope)
		walker.raw(creative.CreativeExtensions, creativeScope, "CreativeExtensions")
		walker.linear(creative.Linear, creativeScope)
		walker.rawElements(creative.UniversalAdIDs, creativeScope, "UniversalAdId")
		walker.anyElements(creative.Other, creativeScope)
	}
}

func (walker namespaceWalker) linear(linear *vastLinear, inherited []xml.Attr) {
	if linear == nil {
		return
	}
	scope := walker.container(&linear.Attrs, inherited)
	if icons := linear.Icons; icons != nil {
		iconScope := walker.container(&icons.Attrs, scope)
		walker.rawElements(icons.Icon, iconScope, "Icon")
	}
	if tracking := linear.TrackingEvents; tracking != nil {
		trackingScope := walker.container(&tracking.Attrs, scope)
		walker.rawElements(tracking.Tracking, trackingScope, "Tracking")
		walker.anyElements(tracking.Other, trackingScope)
	}
	walker.raw(linear.AdParameters, scope, "AdParameters")
	walker.raw(linear.Duration, scope, "Duration")
	walker.raw(linear.MediaFiles, scope, "MediaFiles")
	if clicks := linear.VideoClicks; clicks != nil {
		clickScope := walker.container(&clicks.Attrs, scope)
		walker.rawElements(clicks.ClickTracking, clickScope, "ClickTracking")
		walker.raw(clicks.ClickThrough, clickScope, "ClickThrough")
		walker.rawElements(clicks.CustomClicks, clickScope, "CustomClick")
		walker.anyElements(clicks.Other, clickScope)
	}
	walker.anyElements(linear.Other, scope)
}

func bindChildNamespace(inner, namespace, parent string) string {
	decoder := xml.NewDecoder(strings.NewReader(inner))
	var output strings.Builder
	depth, copied := 0, 0
	for {
		token, err := decoder.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return inner
		}
		switch element := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				hasDefault := false
				if namespace == "" || namespace == "http://www.iab.com/VAST" {
					hasDefault = element.Name.Space == "" && standardRawChild(parent, element.Name.Local)
				}
				for _, attr := range element.Attr {
					if attr.Name == (xml.Name{Local: "xmlns"}) {
						hasDefault = true
					}
				}
				if !hasDefault {
					position := int(decoder.InputOffset()) - 1
					if inner[position-1] == '/' {
						position--
					}
					output.WriteString(inner[copied:position])
					output.WriteString(` xmlns="`)
					output.WriteString(xmlAttrEscape(namespace))
					output.WriteByte('"')
					copied = position
				}
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	output.WriteString(inner[copied:])
	return output.String()
}

func standardRawChild(parent, child string) bool {
	var children []string
	switch parent {
	case "MediaFiles":
		children = []string{"MediaFile", "Mezzanine", "InteractiveCreativeFile", "ClosedCaptionFiles"}
	case "Verification":
		children = []string{"ExecutableResource", "JavaScriptResource", "TrackingEvents", "VerificationParameters"}
	case "CreativeExtensions":
		children = []string{"CreativeExtension"}
	case "Icon":
		children = []string{"IconClicks", "IconViewTracking", "StaticResource", "IFrameResource", "HTMLResource"}
	}
	return slices.Contains(children, child)
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
