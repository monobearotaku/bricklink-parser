package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	"bricklink/parser/internal/config"
	"bricklink/parser/internal/domain"
	"bricklink/parser/internal/domain/task"
	"bricklink/parser/internal/proxy"
	"bricklink/parser/internal/queue"

	"sync/atomic"

	log "github.com/sirupsen/logrus"
	"go.uber.org/ratelimit"
	"resty.dev/v3"
)

type BrickLinkClient interface {
	GetAllCatalogPages(ctx context.Context, categoryType domain.CategoryType) (*domain.CatalogResults, error)
	GetAllCatalogPagesCh(ctx context.Context, categoryType domain.CategoryType, startPage int) (*domain.CatalogResults, chan *domain.CatalogPage, error)
	GetCatalogPage(ctx context.Context, categoryType domain.CategoryType, pageNumber int) (*domain.CatalogPage, error)
	GetItemDetails(ctx context.Context, itemType domain.CategoryType, itemID string) (*domain.PartDetails, error)
}

type brickLinkClient struct {
	rl            ratelimit.Limiter
	config        config.BrickLinkConfig
	baseURL       string
	httpClient    *resty.Client
	parser        *catalogParser
	proxySupplier proxy.ProxySupplier
	queue         queue.Queue

	// Circuit breaker for quota exceeded
	circuitBreakerMutex sync.RWMutex
	quotaExceededUntil  time.Time
	circuitBreakerDelay time.Duration
}

func NewBrickLinkClient(cfg config.BrickLinkConfig, proxySupplier proxy.ProxySupplier, queue queue.Queue) BrickLinkClient {
	client := resty.New().
		SetTimeout(60*time.Second).
		SetRetryCount(3).
		SetRetryWaitTime(2*time.Second).
		SetRetryMaxWaitTime(10*time.Second).
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36").
		SetHeader("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8").
		SetHeader("Accept-Language", "en-US,en;q=0.5").
		SetTLSClientConfig(&tls.Config{
			InsecureSkipVerify: true,
		})

	// Get initial proxy
	if proxySupplier != nil {
		if proxyURL := proxySupplier.Get(); proxyURL != "" {
			client.SetProxy(proxyURL)
			log.Infof("🔗 Using initial proxy: %s", proxyURL)
		}
	}

	return &brickLinkClient{
		rl:                  ratelimit.New(cfg.MaxRequestsPerSecond),
		config:              cfg,
		baseURL:             cfg.BaseURL,
		httpClient:          client,
		parser:              newCatalogParser(cfg.BaseURL),
		proxySupplier:       proxySupplier,
		queue:               queue,
		circuitBreakerDelay: 30 * time.Minute,
	}
}

func (c *brickLinkClient) GetCatalogPage(ctx context.Context, categoryType domain.CategoryType, pageNumber int) (*domain.CatalogPage, error) {
	url := fmt.Sprintf("%s/catalogList.asp?catType=%s&catLike=W&pg=%d",
		c.baseURL,
		categoryType.String(), pageNumber)

	html, err := c.fetchHTML(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch HTML for catalog page: %w", err)
	}

	page, err := c.parser.ParseCatalogPage(html, categoryType)
	if err != nil {
		return nil, fmt.Errorf("failed to parse catalog page: %w", err)
	}

	log.Debugf("Successfully fetched and parsed page %d with %d items", page.PageNumber, len(page.Items))
	return page, nil
}

func (c *brickLinkClient) GetAllCatalogPages(ctx context.Context, categoryType domain.CategoryType) (*domain.CatalogResults, error) {
	results := &domain.CatalogResults{
		CategoryType: categoryType,
		Pages:        make([]*domain.CatalogPage, 0),
	}

	firstPage, err := c.GetCatalogPage(ctx, categoryType, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch first page: %w", err)
	}

	results.TotalItems = firstPage.TotalItems
	results.TotalPages = firstPage.TotalPages

	results.Pages = append(results.Pages, firstPage)

	if firstPage.TotalPages > 1 {
		pagesChan := make(chan *domain.CatalogPage, firstPage.TotalPages-1)
		wg := &sync.WaitGroup{}
		semaphore := make(chan struct{}, c.config.MaxWorkers)

		for pageNum := 2; pageNum <= firstPage.TotalPages; pageNum++ {
			wg.Add(1)

			go func(pageNum int) {
				defer wg.Done()

				semaphore <- struct{}{}
				defer func() { <-semaphore }()

				page, err := c.GetCatalogPage(ctx, categoryType, pageNum)
				if err != nil {
					// Add to retry queue instead of just logging
					retryTask := &task.PageRetryTask{
						PageNumber:   pageNum,
						CategoryType: categoryType,
						RetryCount:   0,
						Error:        err.Error(),
					}

					if c.queue != nil {
						if _, addErr := c.queue.AddTask(ctx, retryTask); addErr != nil {
							log.Errorf("❌ Failed to add page %d to retry queue: %v", pageNum, addErr)
						} else {
							log.Warnf("🔄 Added page %d to retry queue due to fetch failure: %v", pageNum, err)
						}
					} else {
						log.Errorf("Failed to fetch page %d: %v", pageNum, err)
					}
					return
				}

				pagesChan <- page
			}(pageNum)
		}

		wg.Wait()
		close(pagesChan)

		for page := range pagesChan {
			results.Pages = append(results.Pages, page)
		}
	}

	return results, nil
}

func (c *brickLinkClient) GetAllCatalogPagesCh(ctx context.Context, categoryType domain.CategoryType, startPage int) (*domain.CatalogResults, chan *domain.CatalogPage, error) {
	results := &domain.CatalogResults{
		CategoryType: categoryType,
		Pages:        make([]*domain.CatalogPage, 0),
	}

	firstPage, err := c.GetCatalogPage(ctx, categoryType, startPage)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch first page: %w", err)
	}

	results.TotalItems = firstPage.TotalItems
	results.TotalPages = firstPage.TotalPages

	pagesChan := make(chan *domain.CatalogPage, firstPage.TotalPages)
	pagesChan <- firstPage

	if firstPage.TotalPages > startPage {
		var atomicPageNumber atomic.Int32
		atomicPageNumber.Store(int32(startPage))

		go func() {
			defer close(pagesChan)

			wg := &sync.WaitGroup{}
			semaphore := make(chan struct{}, c.config.MaxWorkers)

			for pageNum := startPage + 1; pageNum <= firstPage.TotalPages; pageNum++ {
				wg.Add(1)

				semaphore <- struct{}{}

				go func(pageNum int) {
					defer wg.Done()

					page, err := c.GetCatalogPage(ctx, categoryType, pageNum)
					if err != nil {
						// Add to retry queue instead of just logging
						retryTask := &task.PageRetryTask{
							PageNumber:   pageNum,
							CategoryType: categoryType,
							RetryCount:   0,
							Error:        err.Error(),
						}

						if c.queue != nil {
							if _, addErr := c.queue.AddTask(ctx, retryTask); addErr != nil {
								log.Errorf("❌ Failed to add page %d to retry queue: %v", pageNum, addErr)
							} else {
								log.Warnf("🔄 Added page %d to retry queue due to fetch failure: %v", pageNum, err)
							}
						} else {
							log.Errorf("Failed to fetch page %d: %v", pageNum, err)
						}
						<-semaphore
						return
					}

					pagesChan <- page

					atomicPageNumber.Add(1)

					if atomicPageNumber.Load()%1000 == 0 {
						log.Infof("Fetched %d pages out of %d for category %s", atomicPageNumber.Load(), firstPage.TotalPages, categoryType)
					}

					<-semaphore
				}(pageNum)
			}

			wg.Wait()
		}()
	}

	return results, pagesChan, nil
}

func (c *brickLinkClient) GetItemDetails(ctx context.Context, itemType domain.CategoryType, itemID string) (*domain.PartDetails, error) {
	url := fmt.Sprintf("%s/v2/catalog/catalogitem.page?%s=%s", c.baseURL, itemType, itemID)

	html, err := c.fetchHTML(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch HTML for item %s: %w", itemID, err)
	}

	details, err := c.parser.ParseItemDetails(html, itemID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse item details: %w", err)
	}

	log.Debugf("Successfully fetched and parsed item details for %s", itemID)
	return details, nil
}

func (c *brickLinkClient) isCircuitBreakerOpen() bool {
	c.circuitBreakerMutex.RLock()
	now := time.Now()
	wasOpen := now.Before(c.quotaExceededUntil)
	wasTriggered := !c.quotaExceededUntil.IsZero()
	c.circuitBreakerMutex.RUnlock()

	// If circuit breaker was triggered but is now expired, log re-enabling
	if !wasOpen && wasTriggered {
		c.circuitBreakerMutex.Lock()
		// Double-check after acquiring write lock
		if !c.quotaExceededUntil.IsZero() && now.After(c.quotaExceededUntil) {
			c.quotaExceededUntil = time.Time{} // Reset to avoid repeated logging
			log.Infof("✅ Circuit breaker automatically re-enabled - requests are now allowed")
		}
		c.circuitBreakerMutex.Unlock()
	}

	return wasOpen
}

func (c *brickLinkClient) triggerCircuitBreaker() {
	c.circuitBreakerMutex.Lock()
	defer c.circuitBreakerMutex.Unlock()

	c.quotaExceededUntil = time.Now().Add(c.circuitBreakerDelay)
	log.Warnf("🚫 Circuit breaker activated! All requests disabled until %v (30 minutes)",
		c.quotaExceededUntil.Format("15:04:05"))
}

func (c *brickLinkClient) getRemainingCircuitBreakerTime() time.Duration {
	c.circuitBreakerMutex.RLock()
	defer c.circuitBreakerMutex.RUnlock()

	remaining := time.Until(c.quotaExceededUntil)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (c *brickLinkClient) fetchHTML(ctx context.Context, url string) (string, error) {
	if c.isCircuitBreakerOpen() {
		remaining := c.getRemainingCircuitBreakerTime()
		log.Debugf("🚫 Request blocked by circuit breaker. Remaining time: %v", remaining.Round(time.Second))
		return "", fmt.Errorf("circuit breaker is open - requests disabled for %v more", remaining.Round(time.Second))
	}

	c.rl.Take()

	// Create a context with a longer timeout for individual requests
	reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	resp, err := c.httpClient.R().
		SetContext(reqCtx).
		Get(url)

	if err != nil {
		// Check if this is a context cancellation from the parent context
		if ctx.Err() != nil {
			return "", fmt.Errorf("request cancelled: %w", ctx.Err())
		}
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}

	if resp.IsError() {
		return "", fmt.Errorf("HTTP error: %d %s", resp.StatusCode(), resp.Status())
	}

	html := resp.String()
	if strings.Contains(html, "Quota Exceeded") {
		log.Warnf("🚫 Rate limit exceeded for URL: %s", url)

		proxyRetrySuccessful := false
		if c.proxySupplier != nil {
			if newProxy := c.proxySupplier.Get(); newProxy != "" {
				log.Infof("🔄 Switching to new proxy: %s", newProxy)
				c.httpClient.SetProxy(newProxy)

				// Retry with new proxy immediately (no sleep)
				log.Infof("🔄 Retrying with new proxy...")

				retryResp, retryErr := c.httpClient.R().
					SetContext(reqCtx).
					Get(url)

				if retryErr == nil && !retryResp.IsError() {
					retryHTML := retryResp.String()
					if !strings.Contains(retryHTML, "Quota Exceeded") {
						log.Infof("✅ Retry successful with new proxy")
						proxyRetrySuccessful = true
						return retryHTML, nil
					}
				}
			}
		}

		// If proxy retry failed or no proxy available, trigger circuit breaker
		if !proxyRetrySuccessful {
			c.triggerCircuitBreaker()
			return "", fmt.Errorf("quota exceeded - circuit breaker activated for 30 minutes")
		}
	}

	return html, nil
}
