package genericvast

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// maxWrappers caps the wrapper chain length to guard against loops.
	maxWrappers = 10
	// maxFetchAttempts is the total number of tries per wrapper hop (1 + 2 retries)
	// on HTTP error or empty response.
	maxFetchAttempts = 3
	// maxVASTBytes bounds the size of a fetched VAST document.
	maxVASTBytes = 5 << 20
	// minRequestBudget is the smallest remaining budget worth issuing a fetch with.
	minRequestBudget = 50 * time.Millisecond
	// defaultUnwrapBudget is used when the request carries no usable tmax.
	defaultUnwrapBudget = time.Second
)

// vastFetcher retrieves a wrapped VAST document. It is a field on the adapter so
// tests can inject a fake without hitting the network.
type vastFetcher func(ctx context.Context, url string, timeout time.Duration, headers http.Header) ([]byte, error)

// newHTTPFetcher returns a vastFetcher backed by the given client, applying a
// per-request timeout and bounding the response size.
func newHTTPFetcher(client *http.Client) vastFetcher {
	return func(ctx context.Context, rawURL string, timeout time.Duration, headers http.Header) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, rawURL)
		}
		return io.ReadAll(io.LimitReader(resp.Body, maxVASTBytes))
	}
}

// unwrapDeadline resolves the instant by which unwrapping must finish, derived from
// the auction tmax and the auction-start timestamp header. Parsed once per auction.
func unwrapDeadline(tmax int64, headers http.Header) time.Time {
	total := time.Duration(tmax) * time.Millisecond
	if total <= 0 {
		total = defaultUnwrapBudget
	}
	start := time.Now()
	if s := headers.Get(auctionStartHeader); s != "" {
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			start = time.UnixMilli(ms)
		}
	}
	return start.Add(total)
}

// unwrapVAST resolves <Wrapper> ads recursively by fetching VASTAdTagURI until an
// <InLine> is reached, merging accumulated wrapper tracking into it and re-marshaling.
// It returns the merged VAST and ok=true on success, or ok=false when the chain must be
// dropped (empty response, fetch/parse error, budget or wrapper-limit exceeded).
func unwrapVAST(fetch vastFetcher, initial []byte, headers http.Header, deadline time.Time) (string, bool) {
	var tracking wrapperTracking
	doc, err := parseVAST(initial)
	if err != nil || len(doc.Ads) == 0 {
		return "", false
	}

	for i := 0; ; i++ {
		ad := &doc.Ads[0]
		if ad.Wrapper == nil {
			if ad.InLine == nil {
				return "", false
			}
			mergeIntoInLine(ad.InLine, tracking)
			merged, err := marshalMergedVAST(doc, ad)
			if err != nil {
				return "", false
			}
			return merged, true
		}
		if i >= maxWrappers {
			return "", false
		}
		tracking.merge(collectWrapperTracking(ad.Wrapper))

		uri := ad.Wrapper.VASTAdTagURI.text()
		if uri == "" {
			return "", false
		}
		_, next, ok := fetchWithRetry(fetch, uri, headers, deadline)
		if !ok {
			return "", false
		}
		doc = next
	}
}

// fetchWithRetry fetches a single wrapper hop, retrying on HTTP error or empty/invalid
// VAST up to maxFetchAttempts while budget remains.
func fetchWithRetry(fetch vastFetcher, uri string, headers http.Header, deadline time.Time) ([]byte, *vastDoc, bool) {
	for range maxFetchAttempts {
		remaining := time.Until(deadline)
		if remaining <= minRequestBudget {
			return nil, nil, false
		}
		body, err := fetch(context.Background(), uri, remaining, headers)
		if err != nil {
			continue
		}
		doc, err := parseVAST(body)
		if err != nil || len(doc.Ads) == 0 {
			continue
		}
		return body, doc, true
	}
	return nil, nil, false
}
