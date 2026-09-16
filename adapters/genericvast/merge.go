package genericvast

import (
	"encoding/xml"
	"slices"
	"strings"
)

// wrapperTracking accumulates the mergeable elements pulled from the <Wrapper> ads
// encountered while unwrapping, to be folded into the resolved <InLine>.
type wrapperTracking struct {
	impressions  []rawEl
	errors       []rawEl
	viewable     []anyEl
	verification []rawEl
	extensions   []rawEl
	creatives    []vastCreative
	adSystems    []string
}

func (t *wrapperTracking) merge(o wrapperTracking) {
	t.impressions = append(t.impressions, o.impressions...)
	t.errors = append(t.errors, o.errors...)
	t.viewable = append(t.viewable, o.viewable...)
	t.verification = append(t.verification, o.verification...)
	t.extensions = append(t.extensions, o.extensions...)
	t.creatives = append(t.creatives, o.creatives...)
	t.adSystems = append(t.adSystems, o.adSystems...)
}

// collectWrapperTracking gathers the mergeable elements from a single <Wrapper>.
func collectWrapperTracking(w *vastWrapper) wrapperTracking {
	t := wrapperTracking{impressions: w.Impressions, errors: w.Errors}
	if as := w.AdSystem.text(); as != "" {
		t.adSystems = []string{as}
	}
	if w.ViewableImpr != nil {
		t.viewable = w.ViewableImpr.Items
	}
	if w.AdVerifications != nil {
		t.verification = w.AdVerifications.Verification
	}
	if w.Extensions != nil {
		t.extensions = w.Extensions.Extension
	}
	if w.Creatives != nil {
		for _, c := range w.Creatives.Creative {
			if c.Linear == nil {
				continue
			}
			t.creatives = append(t.creatives, c)
		}
	}
	return t
}

// mergeIntoInLine folds the accumulated wrapper tracking into the resolved <InLine>,
// extending existing containers and creating missing ones.
func mergeIntoInLine(in *vastInLine, t wrapperTracking) {
	in.Impressions = append(in.Impressions, t.impressions...)
	in.Errors = append(in.Errors, t.errors...)

	// AdSystem: prepend each wrapper hop's value to the InLine's own, comma-joined.
	if len(t.adSystems) > 0 {
		parts := append([]string(nil), t.adSystems...)
		if own := in.AdSystem.text(); own != "" {
			parts = append(parts, own)
		}
		in.AdSystem = &rawEl{Inner: xmlAttrEscape(strings.Join(parts, ","))}
	}
	if len(t.viewable) > 0 {
		if in.ViewableImpr == nil {
			in.ViewableImpr = &viewableImpression{}
		}
		in.ViewableImpr.Items = append(in.ViewableImpr.Items, t.viewable...)
		order := map[string]int{"Viewable": 0, "NotViewable": 1, "ViewUndetermined": 2}
		slices.SortStableFunc(in.ViewableImpr.Items, func(left, right anyEl) int { return order[left.XMLName.Local] - order[right.XMLName.Local] })
	}
	if len(t.verification) > 0 {
		if in.AdVerifications == nil {
			in.AdVerifications = &adVerifications{}
		}
		in.AdVerifications.Verification = append(in.AdVerifications.Verification, t.verification...)
	}
	if len(t.extensions) > 0 {
		if in.Extensions == nil {
			in.Extensions = &extensions{}
		}
		in.Extensions.Extension = append(in.Extensions.Extension, t.extensions...)
	}
	if in.Creatives == nil {
		return
	}
	for index := range in.Creatives.Creative {
		creative := &in.Creatives.Creative[index]
		if creative.Linear == nil {
			continue
		}
		for _, wrapper := range slices.Backward(t.creatives) {
			if wrapper.Sequence == "" || wrapper.Sequence == creative.Sequence {
				mergeIcons(creative.Linear, wrapper.Linear)
			}
		}
		for _, wrapper := range t.creatives {
			if wrapper.Sequence != "" && wrapper.Sequence != creative.Sequence {
				continue
			}
			mergeLinearTracking(creative.Linear, wrapper.Linear)
		}
	}
}

func mergeLinearTracking(terminal, wrapper *vastLinear) {
	if wrapper.TrackingEvents != nil && len(wrapper.TrackingEvents.Tracking) > 0 {
		if terminal.TrackingEvents == nil {
			terminal.TrackingEvents = &trackingEvents{}
		}
		terminal.TrackingEvents.Tracking = append(terminal.TrackingEvents.Tracking, wrapper.TrackingEvents.Tracking...)
	}
	if wrapper.VideoClicks != nil && (len(wrapper.VideoClicks.ClickTracking) > 0 || len(wrapper.VideoClicks.CustomClicks) > 0) {
		if terminal.VideoClicks == nil {
			terminal.VideoClicks = &videoClicks{}
		}
		terminal.VideoClicks.ClickTracking = append(terminal.VideoClicks.ClickTracking, wrapper.VideoClicks.ClickTracking...)
		terminal.VideoClicks.CustomClicks = append(terminal.VideoClicks.CustomClicks, wrapper.VideoClicks.CustomClicks...)
	}
}

func mergeIcons(terminal, wrapper *vastLinear) {
	if wrapper.Icons == nil {
		return
	}
	if terminal.Icons == nil {
		terminal.Icons = &vastIcons{}
	}
	programs := make(map[string]bool)
	for _, icon := range terminal.Icons.Icon {
		programs[iconProgram(icon)] = true
	}
	for _, icon := range wrapper.Icons.Icon {
		program := iconProgram(icon)
		if program == "" || !programs[program] {
			terminal.Icons.Icon = append(terminal.Icons.Icon, icon)
			programs[program] = true
		}
	}
}

func iconProgram(icon rawEl) string {
	for _, attr := range icon.Attrs {
		if attr.Name == (xml.Name{Local: "program"}) {
			return attr.Value
		}
	}
	return ""
}

// outVAST is the marshaling envelope for a single merged inline ad.
type outVAST struct {
	XMLName xml.Name   `xml:"VAST"`
	Version string     `xml:"version,attr,omitempty"`
	Attrs   []xml.Attr `xml:",any,attr"`
	Ad      outAd      `xml:"Ad"`
}

type outAd struct {
	ID       string      `xml:"id,attr,omitempty"`
	Sequence string      `xml:"sequence,attr,omitempty"`
	Attrs    []xml.Attr  `xml:",any,attr"`
	InLine   *vastInLine `xml:"InLine"`
}

// marshalMergedVAST serializes a merged inline ad back to a VAST document.
func marshalMergedVAST(doc *vastDoc, ad *vastAd) (string, error) {
	version := doc.Version
	if version == "" {
		version = "4.0"
	}
	target := ""
	for _, attr := range doc.Attrs {
		if attr.Name == (xml.Name{Local: "xmlns"}) {
			target = attr.Value
		}
	}
	walker := namespaceWalker{output: true, target: target}
	walker.ad(ad, nil)
	out := outVAST{
		Version: version,
		Attrs:   marshalAttrs(doc.Attrs),
		Ad:      outAd{ID: ad.ID, Sequence: ad.Sequence, Attrs: marshalAttrs(ad.Attrs), InLine: ad.InLine},
	}
	b, err := xml.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalAttrs(source []xml.Attr) []xml.Attr {
	attrs := make([]xml.Attr, len(source))
	for index, attr := range source {
		if attr.Name.Space == "xmlns" {
			attr.Name = xml.Name{Local: "xmlns:" + attr.Name.Local}
		}
		attrs[index] = attr
	}
	return attrs
}
