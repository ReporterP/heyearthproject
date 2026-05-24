package biz

// discovery.go — synchronous domain auto-discovery for direct routing.
//
// Pipeline: validate root domain → HTTP GET crt.sh certificate transparency
// (gets all certificate SANs for the domain) → parse + dedupe subdomain
// candidates → 20-worker pool calls net.Resolver.LookupIP on each → aggregate
// A and AAAA records into the result.
//
// Hard limits: 500 subdomains processed, 1000 IPs total, 30s total budget,
// 8s for crt.sh, 3s per DNS lookup. Excess is dropped with a warning.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidDomain = errors.New("invalid root domain")
	ErrNoResults     = errors.New("no subdomains found and DNS resolution failed")

	rootDomainRegex = regexp.MustCompile(`^[a-z0-9.-]+\.[a-z][a-z0-9-]+$`)
)

const (
	crtShTimeout   = 8 * time.Second
	dnsLookupTime  = 3 * time.Second
	totalBudget    = 30 * time.Second
	dnsWorkerCount = 20
	maxSubdomains  = 500
	maxIPsCombined = 1000
)

// DiscoveryResult is the response shape for one DiscoverDomain call.
type DiscoveryResult struct {
	Slug               string   `json:"slug"`
	Name               string   `json:"name"`
	GroupName          string   `json:"group_name"`
	Source             string   `json:"source"`
	Domains            []string `json:"domains"`
	IP4                []string `json:"ip4"`
	IP6                []string `json:"ip6"`
	Warnings           []string `json:"warnings"`
	SubdomainsFound    int      `json:"subdomains_found"`
	SubdomainsResolved int      `json:"subdomains_resolved"`
	DurationMs         int64    `json:"duration_ms"`
}

// DiscoverDomain runs the full pipeline. Caller passes the parent context;
// this function applies its own 30s budget on top.
func DiscoverDomain(parent context.Context, rootDomain string) (*DiscoveryResult, error) {
	start := time.Now()

	root, err := normaliseRootDomain(rootDomain)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(parent, totalBudget)
	defer cancel()

	result := &DiscoveryResult{
		Slug:      root,
		Name:      root,
		GroupName: "discovered",
		Source:    "crt.sh",
		Warnings:  []string{},
	}

	// Fetch crt.sh.
	subdomains, crtErr := fetchCrtSh(ctx, root)
	if crtErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("crt.sh failed: %v — will use DNS on root only", crtErr))
	}

	// Always include root domain.
	subSet := map[string]struct{}{root: {}}
	for _, s := range subdomains {
		subSet[s] = struct{}{}
	}

	// Cap input.
	allSubs := make([]string, 0, len(subSet))
	for s := range subSet {
		allSubs = append(allSubs, s)
	}
	sort.Strings(allSubs)
	if len(allSubs) > maxSubdomains {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("crt.sh returned %d subdomains, capped to %d", len(allSubs), maxSubdomains))
		allSubs = allSubs[:maxSubdomains]
	}
	result.SubdomainsFound = len(allSubs)

	// DNS resolve in worker pool.
	ip4Set, ip6Set, resolved := resolveAll(ctx, allSubs)
	result.SubdomainsResolved = resolved

	// Cap output.
	ip4Sorted := setToSortedSlice(ip4Set)
	ip6Sorted := setToSortedSlice(ip6Set)
	if total := len(ip4Sorted) + len(ip6Sorted); total > maxIPsCombined {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("resolved %d IPs, capped to %d", total, maxIPsCombined))
		if len(ip4Sorted) > maxIPsCombined {
			ip4Sorted = ip4Sorted[:maxIPsCombined]
			ip6Sorted = ip6Sorted[:0]
		} else {
			ip6Sorted = ip6Sorted[:maxIPsCombined-len(ip4Sorted)]
		}
	}
	result.Domains = allSubs
	result.IP4 = ip4Sorted
	result.IP6 = ip6Sorted
	result.DurationMs = time.Since(start).Milliseconds()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Warnings = append(result.Warnings, "discovery timed out at 30s, returning partial results")
	}

	if len(result.Domains) <= 1 && len(result.IP4) == 0 && len(result.IP6) == 0 {
		return nil, ErrNoResults
	}

	return result, nil
}

func normaliseRootDomain(in string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(in))
	d = strings.TrimPrefix(d, "www.")
	if d == "" || len(d) > 100 {
		return "", fmt.Errorf("%w: empty or too long", ErrInvalidDomain)
	}
	if net.ParseIP(d) != nil {
		return "", fmt.Errorf("%w: looks like an IP address", ErrInvalidDomain)
	}
	if !rootDomainRegex.MatchString(d) {
		return "", fmt.Errorf("%w: must match host.tld pattern", ErrInvalidDomain)
	}
	return d, nil
}

func fetchCrtSh(ctx context.Context, root string) ([]string, error) {
	q := url.QueryEscape("%." + root)
	uri := "https://crt.sh/?q=" + q + "&output=json"

	reqCtx, cancel := context.WithTimeout(ctx, crtShTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "HiEarth-direct-routing-discovery/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("crt.sh HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var entries []struct {
		NameValue string `json:"name_value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	seen := map[string]struct{}{}
	for _, e := range entries {
		for _, raw := range strings.Split(e.NameValue, "\n") {
			n := strings.ToLower(strings.TrimSpace(raw))
			n = strings.TrimPrefix(n, "*.")
			if n == "" {
				continue
			}
			if !strings.HasSuffix(n, "."+root) && n != root {
				continue
			}
			seen[n] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out, nil
}

func resolveAll(ctx context.Context, subs []string) (map[string]struct{}, map[string]struct{}, int) {
	resolver := &net.Resolver{PreferGo: true}

	ip4 := map[string]struct{}{}
	ip6 := map[string]struct{}{}
	var mu sync.Mutex
	var resolved int

	jobs := make(chan string)
	var wg sync.WaitGroup

	for i := 0; i < dnsWorkerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for host := range jobs {
				lookupCtx, cancel := context.WithTimeout(ctx, dnsLookupTime)
				ips, err := resolver.LookupIP(lookupCtx, "ip", host)
				cancel()
				if err != nil || len(ips) == 0 {
					continue
				}
				mu.Lock()
				resolved++
				for _, ip := range ips {
					if v4 := ip.To4(); v4 != nil {
						ip4[v4.String()] = struct{}{}
					} else if v6 := ip.To16(); v6 != nil {
						ip6[v6.String()] = struct{}{}
					}
				}
				mu.Unlock()
			}
		}()
	}

	for _, s := range subs {
		select {
		case <-ctx.Done():
			break
		case jobs <- s:
		}
	}
	close(jobs)
	wg.Wait()
	return ip4, ip6, resolved
}

func setToSortedSlice(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
