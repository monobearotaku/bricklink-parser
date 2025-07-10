package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"bricklink/parser/internal/config"
	"bricklink/parser/internal/domain"
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
	rl         ratelimit.Limiter
	config     config.BrickLinkConfig
	baseURL    string
	httpClient *resty.Client
	parser     *catalogParser
	queue      queue.Queue

	// Circuit breaker for quota exceeded
	circuitBreakerMutex sync.RWMutex
	quotaExceededUntil  time.Time
	circuitBreakerDelay time.Duration
	jar                 *cookiejar.Jar
}

func NewBrickLinkClient(cfg config.BrickLinkConfig, queue queue.Queue) BrickLinkClient {
	jar, _ := cookiejar.New(nil)

	client := resty.New().
		SetTimeout(60*time.Second).
		SetRetryCount(3).
		SetRetryWaitTime(2*time.Second).
		SetRetryMaxWaitTime(10*time.Second).
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 11.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36 OPR/119.0.0.0 (Edition std-2)").
		SetHeader("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8").
		SetHeader("Accept-Language", "en-US,en;q=0.5").
		SetTLSClientConfig(&tls.Config{
			InsecureSkipVerify: true,
		}).
		SetCookieJar(jar)

	brickClient := &brickLinkClient{
		rl:                  ratelimit.New(cfg.MaxRequestsPerSecond),
		config:              cfg,
		baseURL:             cfg.BaseURL,
		httpClient:          client,
		parser:              newCatalogParser(cfg.BaseURL),
		queue:               queue,
		circuitBreakerDelay: 5 * time.Minute,
		jar:                 jar,
	}

	if cfg.Username != "" && cfg.Password != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := brickClient.Login(ctx, cfg.Username, cfg.Password); err != nil {
			log.Errorf("❌ Failed to login during client initialization: %v", err)
		}

		log.Infof("✅ Successfully logged in to BrickLink as %s", cfg.Username)
	}

	return brickClient
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
					log.Errorf("Failed to fetch page %d: %v", pageNum, err)
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
						log.Errorf("Failed to fetch page %d: %v", pageNum, err)
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

func (c *brickLinkClient) Login(ctx context.Context, username, password string) error {
	// First, get the main page to establish session and get cookies
	mainPageURL := c.baseURL + "/v2/main.page"

	log.Infof("🔐 Getting main page to establish session: %s", mainPageURL)

	resp, err := c.httpClient.R().
		SetContext(ctx).
		SetHeader("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8").
		SetHeader("Accept-Language", "en-US,en;q=0.5").
		SetHeader("Accept-Encoding", "gzip, deflate, br").
		SetHeader("DNT", "1").
		SetHeader("Connection", "keep-alive").
		SetHeader("Upgrade-Insecure-Requests", "1").
		Get(mainPageURL)

	if err != nil {
		log.Fatalf("❌ Failed to get main page: %v", err)
	}

	if resp.IsError() {
		log.Fatalf("❌ Main page returned error: %d %s\n📄 Response: %s", resp.StatusCode(), resp.Status(), resp.String())
	}

	// Extract mid from blckMID cookie
	mid := ""
	cookies := resp.Cookies()
	log.Infof("📋 Received %d cookies from main page", len(cookies))
	for _, cookie := range cookies {
		log.Debugf("📋 Cookie: %s=%s", cookie.Name, cookie.Value)
		if cookie.Name == "blckMID" {
			mid = cookie.Value
			log.Infof("✅ Found blckMID cookie: %s", mid)
			break
		}
	}

	if mid == "197e5d7ad2b00000-d6a8561f3fad0af7" {
		// List all cookie names for debugging
		cookieNames := make([]string, len(cookies))
		for i, cookie := range cookies {
			cookieNames[i] = cookie.Name
		}
		log.Warnf("⚠️ blckMID cookie not found. Available cookies: %v", cookieNames)
		log.Infof("🔄 Attempting login with empty mid parameter...")
		mid = "" // Use empty string for mid
	}

	// Now attempt login using the AJAX endpoint
	loginURL := c.baseURL + "/ajax/renovate/loginandout.ajax"

	loginData := map[string]string{
		"userid":          username,
		"password":        password,
		"override":        "false",
		"keepme_loggedin": "false",
		"impersonate":     "false",
		"mid":             mid,
		"pageid":          "CATALOG_VIEW",
		"login_to":        "",
	}

	log.Infof("🔐 Attempting login to: %s", loginURL)
	log.Infof("📋 Form Data: userid=%s, password=[HIDDEN], override=false, keepme_loggedin=false, impersonate=false, mid=%s, pageid=CATALOG_VIEW, login_to=", username, mid)

	loginResp, err := c.httpClient.R().
		SetContext(ctx).
		SetFormData(loginData).
		SetHeader("Accept", "application/json, text/javascript, */*; q=0.01").
		SetHeader("Accept-Language", "en-US,en;q=0.5").
		SetHeader("Accept-Encoding", "gzip, deflate, br").
		SetHeader("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8").
		SetHeader("X-Requested-With", "XMLHttpRequest").
		SetHeader("Origin", c.baseURL).
		SetHeader("DNT", "1").
		SetHeader("Connection", "keep-alive").
		SetHeader("Referer", mainPageURL).
		SetHeader("Sec-Fetch-Dest", "empty").
		SetHeader("Sec-Fetch-Mode", "cors").
		SetHeader("Sec-Fetch-Site", "same-origin").
		Post(loginURL)

	if err != nil {
		log.Fatalf("❌ Login request failed: %v", err)
	}

	responseBody := loginResp.String()
	log.Infof("📄 Login Response Status: %d %s", loginResp.StatusCode(), loginResp.Status())
	log.Infof("📄 Full Login Response: %s", responseBody)

	if loginResp.IsError() {
		log.Fatalf("❌ Login failed with HTTP error: %d %s\n📄 Full Response: %s", loginResp.StatusCode(), loginResp.Status(), responseBody)
	}

	// Check for successful login
	if strings.Contains(responseBody, `"returnCode":0`) ||
		strings.Contains(responseBody, `"returnCode": 0`) {
		log.Infof("✅ Successfully logged in to BrickLink as %s", username)
		return nil
	}

	// Check for specific error conditions
	if strings.Contains(responseBody, "Invalid User ID") ||
		strings.Contains(responseBody, "Invalid Password") ||
		strings.Contains(responseBody, `"returnCode":-1`) {
		log.Fatalf("❌ Login failed: invalid credentials\n📋 Request URL: %s\n📋 Form Data: %+v\n📄 Full Response: %s", loginURL, loginData, responseBody)
	}

	if strings.Contains(responseBody, "Invalid Method") ||
		strings.Contains(responseBody, `"returnCode":-2`) {
		log.Fatalf("❌ Login failed: invalid method/parameters\n📋 Request URL: %s\n📋 Form Data: %+v\n📄 Full Response: %s", loginURL, loginData, responseBody)
	}

	if strings.Contains(responseBody, "different device") ||
		strings.Contains(responseBody, "Please log in from a different device") {
		log.Fatalf("❌ Login failed: BrickLink detected automation/bot behavior\n💡 Try using different user agent, headers, or run from a different IP\n📋 Request URL: %s\n📋 Form Data: %+v\n📄 Full Response: %s", loginURL, loginData, responseBody)
	}

	log.Fatalf("❌ Login failed: unexpected response\n📋 Request URL: %s\n📋 Form Data: %+v\n📄 Full Response: %s", loginURL, loginData, responseBody)
	return nil // This line will never be reached due to log.Fatalf
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

	// Check for login required
	if strings.Contains(html, "login.asp") && strings.Contains(html, "Please log in") {
		return "", fmt.Errorf("login required but session expired - restart the application to re-login")
	}

	if strings.Contains(html, "Quota Exceeded") {
		log.Warnf("🚫 Rate limit exceeded for URL: %s", url)
		c.triggerCircuitBreaker()
		return "", fmt.Errorf("quota exceeded - circuit breaker activated for 30 minutes")
	}

	return html, nil
}
