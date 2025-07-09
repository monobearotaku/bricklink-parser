package proxy

import (
	"context"
	"crypto/tls"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"resty.dev/v3"
)

// ProxySupplier manages a pool of proxies with round-robin selection
type ProxySupplier interface {
	Get() string
}

type proxySupplier struct {
	proxies []string
	current int
	mutex   sync.Mutex
}

// NewProxySupplier creates a new ProxySupplier with validated proxies
func NewProxySupplier(ctx context.Context, proxies []string, testURL string) (ProxySupplier, error) {
	if len(proxies) == 0 {
		return &proxySupplier{proxies: []string{}, current: 0}, nil
	}

	validProxies := make([]string, 0, len(proxies))
	validProxiesCh := make(chan string, len(proxies))

	log.Infof("🔄 Testing %d proxies in parallel...", len(proxies))

	semaphore := make(chan struct{}, 50)

	var wg sync.WaitGroup

	for i, proxyURL := range proxies {
		wg.Add(1)

		go func(index int, proxy string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			log.Debugf("🔄 Testing proxy %d/%d: %s", index+1, len(proxies), proxy)

			if isProxyValid(ctx, proxy, testURL) {
				validProxiesCh <- proxy
				log.Infof("✅ Proxy %s is working", proxy)
			} else {
				log.Infof("❌ Proxy %s is not working, skipping", proxy)
			}
		}(i, proxyURL)
	}

	// Wait for all proxy tests to complete
	wg.Wait()
	close(validProxiesCh)

	for proxy := range validProxiesCh {
		validProxies = append(validProxies, proxy)
	}

	log.Infof("✅ ProxySupplier initialized with %d working proxies out of %d tested", len(validProxies), len(proxies))

	return &proxySupplier{
		proxies: validProxies,
		current: 0,
	}, nil
}

// Get returns the next proxy URL in round-robin fashion
func (p *proxySupplier) Get() string {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if len(p.proxies) == 0 {
		return "" // No proxies available
	}

	proxy := p.proxies[p.current]
	p.current = (p.current + 1) % len(p.proxies)

	return proxy
}

// isProxyValid tests if a proxy can successfully make a request to the test URL
func isProxyValid(ctx context.Context, proxyURL, testURL string) bool {
	client := resty.New().
		SetTimeout(5 * time.Second). // Reduced timeout for faster testing
		SetRetryCount(0).            // No retries for faster testing
		SetProxy(proxyURL).
		SetTLSClientConfig(&tls.Config{
			InsecureSkipVerify: true,
		})

	resp, err := client.R().
		SetContext(ctx).
		Get(testURL)

	if err != nil {
		log.Infof("Proxy test failed for %s: %v", proxyURL, err)
		return false
	}

	if resp.IsError() {
		log.Infof("Proxy test failed for %s with status: %s", proxyURL, resp.Status())
		return false
	}

	return true
}
