package genericvast

import (
	"encoding/xml"
	"strings"
)

// wrapperTracking accumulates the mergeable elements pulled from the <Wrapper> ads
// encountered while unwrapping, to be folded into the resolved <InLine>.
type wrapperTracking struct {
	impressions   []rawEl
	errors        []rawEl
	viewable      []anyEl
	verification  []rawEl
	extensions    []rawEl
	tracking      []rawEl
	clickTracking []rawEl
	adSystems     []string
}

func (t *wrapperTracking) merge(o wrapperTracking) {
	t.impressions = append(t.impressions, o.impressions...)
	t.errors = append(t.errors, o.errors...)
	t.viewable = append(t.viewable, o.viewable...)
	t.verification = append(t.verification, o.verification...)
	t.extensions = append(t.extensions, o.extensions...)
	t.tracking = append(t.tracking, o.tracking...)
	t.clickTracking = append(t.clickTracking, o.clickTracking...)
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
			if c.Linear.TrackingEvents != nil {
				t.tracking = append(t.tracking, c.Linear.TrackingEvents.Tracking...)
			}
			if c.Linear.VideoClicks != nil {
				t.clickTracking = append(t.clickTracking, c.Linear.VideoClicks.ClickTracking...)
			}
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
	if len(t.tracking) > 0 || len(t.clickTracking) > 0 {
		if lin := in.firstLinear(); lin != nil {
			if len(t.tracking) > 0 {
				if lin.TrackingEvents == nil {
					lin.TrackingEvents = &trackingEvents{}
				}
				lin.TrackingEvents.Tracking = append(lin.TrackingEvents.Tracking, t.tracking...)
			}
			if len(t.clickTracking) > 0 {
				if lin.VideoClicks == nil {
					lin.VideoClicks = &videoClicks{}
				}
				lin.VideoClicks.ClickTracking = append(lin.VideoClicks.ClickTracking, t.clickTracking...)
			}
		}
	}
}

// firstLinear returns the first <Linear> across the InLine's creatives, or nil.
func (in *vastInLine) firstLinear() *vastLinear {
	if in.Creatives == nil {
		return nil
	}
	for i := range in.Creatives.Creative {
		if in.Creatives.Creative[i].Linear != nil {
			return in.Creatives.Creative[i].Linear
		}
	}
	return nil
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
	InLine   *vastInLine `xml:"InLine"`
}

// marshalMergedVAST serializes a merged inline ad back to a VAST document.
func marshalMergedVAST(doc *vastDoc, ad *vastAd) (string, error) {
	version := doc.Version
	if version == "" {
		version = "4.0"
	}
	attrs := make([]xml.Attr, len(doc.Attrs))
	for index, attr := range doc.Attrs {
		if attr.Name.Space == "xmlns" {
			attr.Name = xml.Name{Local: "xmlns:" + attr.Name.Local}
		}
		attrs[index] = attr
	}
	out := outVAST{Version: version, Attrs: attrs, Ad: outAd{ID: ad.ID, Sequence: ad.Sequence, InLine: ad.InLine}}
	b, err := xml.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
