package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"time"

	vegeta "github.com/tsenart/vegeta/v12/lib"
	"url-shortener/internal/helpers"
)

var (
	userAgents = []string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:129.0) Gecko/20100101 Firefox/129.0",
		"Mozilla/5.0 (iPad; CPU OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.6613.88 Mobile Safari/537.36",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
	}

	referrers = []string{
		"https://twitter.com/dev/status/123",
		"https://news.ycombinator.com/item?id=456",
		"https://reddit.com/r/golang",
		"https://linkedin.com/feed",
		"https://github.com/golang/go",
		"",
	}

	countries = []string{"US", "GB", "IN", "DE", "JP", "FR", "CA", "SG"}
)

func randomHeaders() http.Header {
	h := make(http.Header)
	h.Set("User-Agent", userAgents[rand.IntN(len(userAgents))])
	if ref := referrers[rand.IntN(len(referrers))]; ref != "" {
		h.Set("Referer", ref)
	}
	h.Set("CF-IPCountry", countries[rand.IntN(len(countries))])
	h.Set("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", rand.IntN(250)+1))
	return h
}

func main() {
	mode := flag.String("mode", "fixed", "Test mode: 'fixed' (single attack) or 'ramp' (exponential probe + binary search to failure)")
	scenario := flag.String("scenario", "redirect", "Load test scenario: redirect, create, analytics, mixed")
	rateFlag := flag.Int("rate", 500, "Requests per second for fixed mode")
	durationFlag := flag.Duration("duration", 10*time.Second, "Duration for fixed mode (e.g. 10s, 30s)")
	baseURLFlag := flag.String("url", "http://localhost:8080", "Base server URL")
	shortKeyFlag := flag.String("key", "godoc2026", "Target short key for redirect test")
	urlIDFlag := flag.String("id", "", "Target URL UUID for analytics test (optional, auto-detected if empty)")
	workersFlag := flag.Uint("workers", 50, "Number of concurrent attacker workers")

	// Binary search / ramp flags
	minRateFlag := flag.Int("min-rate", 100, "Starting rate for ramp mode")
	maxRateFlag := flag.Int("max-rate", 10000, "Maximum search boundary for ramp mode")
	stepDurFlag := flag.Duration("step-duration", 5*time.Second, "Duration of each step in ramp mode")
	maxP95Flag := flag.Duration("max-p95", 50*time.Millisecond, "Max acceptable p95 latency before considering a step failed")
	maxErrFlag := flag.Float64("max-error-rate", 0.01, "Max acceptable error rate (0.01 = 1%)")
	flag.Parse()

	baseURL := strings.TrimSuffix(*baseURLFlag, "/")

	// If scenario is analytics or mixed and urlID is empty, try to resolve ID from key
	targetURLID := *urlIDFlag
	if (*scenario == "analytics" || *scenario == "mixed") && targetURLID == "" {
		targetURLID = resolveURLID(baseURL, *shortKeyFlag)
		if targetURLID != "" {
			fmt.Printf("ℹ️  Resolved URL UUID for analytics: %s\n\n", targetURLID)
		}
	}

	if *mode == "ramp" {
		runBinarySearch(*scenario, baseURL, *shortKeyFlag, targetURLID,
			*minRateFlag, *maxRateFlag, *stepDurFlag, *maxP95Flag, *maxErrFlag, uint64(*workersFlag))
		return
	}

	// Fixed Mode
	runFixedTest(*scenario, baseURL, *shortKeyFlag, targetURLID, *rateFlag, *durationFlag, uint64(*workersFlag))
}

func runFixedTest(scenario, baseURL, shortKey, urlID string, rate int, duration time.Duration, workers uint64) {
	fmt.Println("=================================================================")
	fmt.Printf("🚀 Starting Vegeta Load Test (Fixed Mode)\n")
	fmt.Printf("   Scenario: %s | Target Rate: %d req/s | Duration: %s\n", scenario, rate, duration)
	fmt.Printf("   Base URL: %s | Workers: %d\n", baseURL, workers)
	fmt.Println("=================================================================")

	targeter := createTargeter(scenario, baseURL, shortKey, urlID)
	attacker := newAttacker(workers)
	pacer := vegeta.ConstantPacer(vegeta.Rate{Freq: rate, Per: time.Second})

	buckets := vegeta.Buckets{
		0,
		1 * time.Millisecond,
		2 * time.Millisecond,
		5 * time.Millisecond,
		10 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		250 * time.Millisecond,
		500 * time.Millisecond,
		1 * time.Second,
	}
	hist := vegeta.Histogram{Buckets: buckets}

	var metrics vegeta.Metrics
	fmt.Printf("⚡ Attacking %s at %d RPS for %s...\n", baseURL, rate, duration)

	start := time.Now()
	for res := range attacker.Attack(targeter, pacer, duration, "URL-Shortener-Benchmark") {
		metrics.Add(res)
		hist.Add(res)
	}
	metrics.Close()
	elapsed := time.Since(start)

	fmt.Printf("\n Attack completed in %s.\n\n", elapsed.Round(time.Millisecond))

	fmt.Println("----------------- VEGETA BENCHMARK RESULTS -----------------")
	reporter := vegeta.NewTextReporter(&metrics)
	if err := reporter.Report(os.Stdout); err != nil {
		fmt.Printf("Error generating text report: %v\n", err)
	}

	fmt.Println("\n----------------- LATENCY DISTRIBUTION HISTOGRAM -----------------")
	histReporter := vegeta.NewHistogramReporter(&hist)
	if err := histReporter.Report(os.Stdout); err != nil {
		fmt.Printf("Error generating histogram report: %v\n", err)
	}
	fmt.Println("------------------------------------------------------------------")
}

func runBinarySearch(scenario, baseURL, shortKey, urlID string, minRate, maxRate int, stepDur, maxP95 time.Duration, maxErrorRate float64, workers uint64) {
	fmt.Println("=================================================================")
	fmt.Printf("🔍 Stress Test: Ramp-Up & Binary Search Capacity Finder\n")
	fmt.Printf("   Scenario: %s | Base URL: %s\n", scenario, baseURL)
	fmt.Printf("   Search Range: [%d, %d] RPS | Step Duration: %s\n", minRate, maxRate, stepDur)
	fmt.Printf("   Thresholds: Max p95 Latency = %s | Max Error Rate = %.1f%%\n", maxP95, maxErrorRate*100)
	fmt.Println("=================================================================")

	targeter := createTargeter(scenario, baseURL, shortKey, urlID)

	type stepResult struct {
		rate       int
		passed     bool
		reqs       uint64
		p50        time.Duration
		p95        time.Duration
		p99        time.Duration
		maxLat     time.Duration
		successPct float64
		codes      map[string]int
		errors     []string
	}

	evaluate := func(rate int) stepResult {
		fmt.Printf("👉 Testing %4d req/s for %s... ", rate, stepDur)
		attacker := newAttacker(workers)
		pacer := vegeta.ConstantPacer(vegeta.Rate{Freq: rate, Per: time.Second})

		var metrics vegeta.Metrics
		for res := range attacker.Attack(targeter, pacer, stepDur, fmt.Sprintf("probe-%d", rate)) {
			metrics.Add(res)
		}
		metrics.Close()

		res := stepResult{
			rate:       rate,
			reqs:       metrics.Requests,
			p50:        metrics.Latencies.P50,
			p95:        metrics.Latencies.P95,
			p99:        metrics.Latencies.P99,
			maxLat:     metrics.Latencies.Max,
			successPct: metrics.Success * 100,
			codes:      metrics.StatusCodes,
			errors:     metrics.Errors,
		}

		errRate := 1.0 - metrics.Success
		if errRate <= maxErrorRate && metrics.Latencies.P95 <= maxP95 && metrics.Requests > 0 {
			res.passed = true
			fmt.Printf("✅ PASS | p50: %6s | p95: %6s | p99: %6s | Success: %5.1f%%\n",
				res.p50.Round(time.Microsecond), res.p95.Round(time.Microsecond), res.p99.Round(time.Microsecond), res.successPct)
		} else {
			res.passed = false
			reason := ""
			if errRate > maxErrorRate {
				reason += fmt.Sprintf("Error rate %.2f%% > %.2f%%; ", errRate*100, maxErrorRate*100)
			}
			if metrics.Latencies.P95 > maxP95 {
				reason += fmt.Sprintf("p95 %s > %s; ", res.p95.Round(time.Millisecond), maxP95)
			}
			fmt.Printf("❌ FAIL | p50: %6s | p95: %6s | Success: %5.1f%% (%s)\n",
				res.p50.Round(time.Microsecond), res.p95.Round(time.Microsecond), res.successPct, strings.TrimRight(reason, "; "))
		}

		// Brief cooldown between steps to allow background goroutines and sockets to recover
		time.Sleep(1 * time.Second)
		return res
	}

	// Phase 1: Exponential probe to find the failure ceiling
	fmt.Println("\n--- Phase 1: Exponential Ramp-Up (Finding Ceiling) ---")
	currentRate := minRate
	lastPassRate := 0
	firstFailRate := 0
	var history []stepResult

	for currentRate <= maxRate {
		res := evaluate(currentRate)
		history = append(history, res)
		if res.passed {
			lastPassRate = currentRate
			currentRate *= 2 // Double the load
		} else {
			firstFailRate = currentRate
			break
		}
	}

	// If even the minimum rate failed:
	if lastPassRate == 0 {
		fmt.Printf("\n⚠️ System failed even at the minimum rate (%d req/s). Cannot binary search upwards.\n", minRate)
		return
	}

	// If maximum search boundary was reached without failing:
	if firstFailRate == 0 {
		fmt.Printf("\n🏆 Incredible! System handled the maximum configured search limit of %d req/s without breaking!\n", maxRate)
		return
	}

	// Phase 2: Binary Search between lastPassRate and firstFailRate
	fmt.Printf("\n--- Phase 2: Binary Search in Range [%d, %d] RPS ---\n", lastPassRate, firstFailRate)
	low := lastPassRate
	high := firstFailRate

	for high-low > 100 {
		mid := (low + high) / 2
		res := evaluate(mid)
		history = append(history, res)
		if res.passed {
			low = mid
			lastPassRate = mid
		} else {
			high = mid
			firstFailRate = mid
		}
	}

	// Final Summary Report
	fmt.Println("\n=================================================================")
	fmt.Printf("🎯 CAPACITY SEARCH RESULTS & BOTTLENECK ANALYSIS\n")
	fmt.Println("=================================================================")
	fmt.Printf("  🏆 Maximum Sustainable Throughput:  %d req/s\n", lastPassRate)
	fmt.Printf("  💥 Point of Failure / Degradation:  %d req/s\n\n", firstFailRate)

	fmt.Println("Progression History:")
	fmt.Printf("  %-8s | %-6s | %-10s | %-10s | %-10s | %-8s\n", "Rate", "Result", "p50", "p95", "p99", "Success")
	fmt.Println("  -------------------------------------------------------------------")
	for _, h := range history {
		status := "PASS"
		if !h.passed {
			status = "FAIL"
		}
		fmt.Printf("  %-8d | %-6s | %-10s | %-10s | %-10s | %6.1f%%\n",
			h.rate, status, h.p50.Round(time.Microsecond), h.p95.Round(time.Microsecond), h.p99.Round(time.Microsecond), h.successPct)
	}

	fmt.Println("\nFailure Analysis at breakpoint:")
	failRes := history[len(history)-1]
	for _, h := range history {
		if !h.passed {
			failRes = h
			break
		}
	}
	fmt.Printf("  Status Codes: %v\n", failRes.codes)
	if len(failRes.errors) > 0 {
		fmt.Printf("  Sample Errors:\n")
		limit := 3
		if len(failRes.errors) < limit {
			limit = len(failRes.errors)
		}
		for i := 0; i < limit; i++ {
			fmt.Printf("   - %s\n", failRes.errors[i])
		}
	}
	fmt.Println("=================================================================")
}

func newAttacker(workers uint64) *vegeta.Attacker {
	transport := &http.Transport{
		MaxIdleConns:        4000,
		MaxIdleConnsPerHost: 2000,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression: true,
	}

	return vegeta.NewAttacker(
		vegeta.Client(&http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: 4 * time.Second,
		}),
		vegeta.Workers(workers),
	)
}

func createTargeter(scenario, baseURL, shortKey, urlID string) vegeta.Targeter {
	switch scenario {
	case "create":
		return func(tgt *vegeta.Target) error {
			key := helpers.GenerateShortKey(7)
			payload := map[string]string{
				"original_url": "https://go.dev/doc/loadtest-" + key,
				"title":        "LoadTest Link " + key,
				"custom_key":   "k_" + key,
			}
			body, _ := json.Marshal(payload)
			tgt.Method = "POST"
			tgt.URL = baseURL + "/api/v1/urls"
			tgt.Header = http.Header{
				"Content-Type": []string{"application/json"},
			}
			tgt.Body = body
			return nil
		}

	case "analytics":
		return func(tgt *vegeta.Target) error {
			id := urlID
			if id == "" {
				id = "01a1458a-3a0b-71bd-9979-7f618847ee9a"
			}
			tgt.Method = "GET"
			tgt.URL = fmt.Sprintf("%s/api/v1/urls/%s/analytics?range=24h", baseURL, id)
			tgt.Header = randomHeaders()
			tgt.Body = nil
			return nil
		}

	case "mixed":
		return func(tgt *vegeta.Target) error {
			roll := rand.IntN(100)
			if roll < 90 {
				// 90% Hot path redirects
				tgt.Method = "GET"
				tgt.URL = baseURL + "/" + shortKey
				tgt.Header = randomHeaders()
				tgt.Body = nil
			} else if roll < 98 {
				// 8% URL creations
				key := helpers.GenerateShortKey(7)
				payload := map[string]string{
					"original_url": "https://go.dev/doc/mixed-" + key,
					"title":        "Mixed Test " + key,
					"custom_key":   "m_" + key,
				}
				body, _ := json.Marshal(payload)
				tgt.Method = "POST"
				tgt.URL = baseURL + "/api/v1/urls"
				tgt.Header = http.Header{
					"Content-Type": []string{"application/json"},
				}
				tgt.Body = body
			} else {
				// 2% Analytics queries
				id := urlID
				if id == "" {
					id = "01a1458a-3a0b-71bd-9979-7f618847ee9a"
				}
				tgt.Method = "GET"
				tgt.URL = fmt.Sprintf("%s/api/v1/urls/%s/analytics?range=24h", baseURL, id)
				tgt.Header = randomHeaders()
				tgt.Body = nil
			}
			return nil
		}

	case "redirect":
		fallthrough
	default:
		return func(tgt *vegeta.Target) error {
			tgt.Method = "GET"
			tgt.URL = baseURL + "/" + shortKey
			tgt.Header = randomHeaders()
			tgt.Body = nil
			return nil
		}
	}
}

func resolveURLID(baseURL, shortKey string) string {
	resp, err := http.Get(baseURL + "/api/v1/urls")
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()

	var data struct {
		URLs []struct {
			ID       string `json:"id"`
			ShortKey string `json:"short_key"`
		} `json:"urls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ""
	}

	for _, u := range data.URLs {
		if u.ShortKey == shortKey {
			return u.ID
		}
	}
	if len(data.URLs) > 0 {
		return data.URLs[0].ID
	}
	return ""
}
