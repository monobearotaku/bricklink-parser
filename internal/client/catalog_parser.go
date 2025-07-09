package client

import (
	"bricklink/parser/internal/domain"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	log "github.com/sirupsen/logrus"
)

type catalogParser struct {
	baseURL string
}

func newCatalogParser(baseURL string) *catalogParser {
	return &catalogParser{
		baseURL: baseURL,
	}
}

func (p *catalogParser) ParseCatalogPage(html string, categoryType domain.CategoryType) (*domain.CatalogPage, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	page := &domain.CatalogPage{
		CategoryType: categoryType,
		Items:        make([]domain.CatalogPageItem, 0),
	}

	page, err = p.extractPaginationInfo(doc, page)
	if err != nil {
		log.Warnf("Failed to extract pagination info: %v", err)
		return nil, err
	}

	page, err = p.extractItems(doc, page)
	if err != nil {
		return nil, fmt.Errorf("failed to extract items: %w", err)
	}

	log.Debugf("Parsed page %d with %d items", page.PageNumber, len(page.Items))
	return page, nil
}

func (p *catalogParser) extractPaginationInfo(doc *goquery.Document, page *domain.CatalogPage) (*domain.CatalogPage, error) {
	// Look for pagination text that contains both "Items Found" and "Page"
	var paginationText string

	// Try different selectors to find pagination info
	doc.Find("*").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if strings.Contains(text, "Items Found") && strings.Contains(text, "Page") && strings.Contains(text, "of") {
			paginationText = text
			return
		}
	})

	// If not found, try looking in document text
	if paginationText == "" {
		fullText := doc.Text()
		// Look for the pagination pattern in the full text
		re := regexp.MustCompile(`(\d+)\s+Items Found.*?Page\s+(\d+)\s+of\s+(\d+).*?Showing\s+(\d+)\s+Items Per Page`)
		matches := re.FindStringSubmatch(fullText)
		if len(matches) >= 5 {
			if totalItems, err := strconv.Atoi(matches[1]); err == nil {
				page.TotalItems = totalItems
			}
			if currentPage, err := strconv.Atoi(matches[2]); err == nil {
				page.PageNumber = currentPage
			}
			if totalPages, err := strconv.Atoi(matches[3]); err == nil {
				page.TotalPages = totalPages
			}
			if itemsPerPage, err := strconv.Atoi(matches[4]); err == nil {
				page.ItemsPerPage = itemsPerPage
			}
			log.Debugf("Found pagination via full text: %d items, page %d of %d", page.TotalItems, page.PageNumber, page.TotalPages)
			return page, nil
		}
	}

	if paginationText != "" {
		log.Debugf("Found pagination text: %s", paginationText)

		// Extract total items: "88618 Items Found"
		totalItemsRegex := regexp.MustCompile(`(\d+)\s+Items Found`)
		if matches := totalItemsRegex.FindStringSubmatch(paginationText); len(matches) > 1 {
			if totalItems, err := strconv.Atoi(matches[1]); err == nil {
				page.TotalItems = totalItems
			}
		}

		// Extract current page and total pages: "Page 1 of 1773"
		pageRegex := regexp.MustCompile(`Page\s+(\d+)\s+of\s+(\d+)`)
		if matches := pageRegex.FindStringSubmatch(paginationText); len(matches) > 2 {
			if currentPage, err := strconv.Atoi(matches[1]); err == nil {
				page.PageNumber = currentPage
			}
			if totalPages, err := strconv.Atoi(matches[2]); err == nil {
				page.TotalPages = totalPages
			}
		}

		// Extract items per page: "Showing 50 Items Per Page"
		itemsPerPageRegex := regexp.MustCompile(`Showing\s+(\d+)\s+Items Per Page`)
		if matches := itemsPerPageRegex.FindStringSubmatch(paginationText); len(matches) > 1 {
			if itemsPerPage, err := strconv.Atoi(matches[1]); err == nil {
				page.ItemsPerPage = itemsPerPage
			}
		}

		log.Debugf("Extracted pagination: %d items, page %d of %d", page.TotalItems, page.PageNumber, page.TotalPages)
		return page, nil
	}

	// If we can't find pagination info, check if we have any items
	// This could be a single page with items but no pagination display
	if len(page.Items) > 0 {
		log.Warnf("No pagination text found, but found %d items. Assuming single page.", len(page.Items))
		page.PageNumber = 1
		page.TotalPages = 1
		page.TotalItems = len(page.Items)
		page.ItemsPerPage = len(page.Items)
		return page, nil
	}

	// If we reach here, there's no pagination and no items - might be an error page
	log.Warnf("No pagination text found and no items found - page might be empty or error")
	return nil, fmt.Errorf("no pagination text found")
}

func (p *catalogParser) extractItems(doc *goquery.Document, page *domain.CatalogPage) (*domain.CatalogPage, error) {
	itemCount := 0

	// Look for links that contain catalog item patterns
	doc.Find("a[href*='/v2/catalog/catalogitem.page']").Each(func(i int, link *goquery.Selection) {
		href, exists := link.Attr("href")
		if !exists {
			return
		}

		// Extract item number from URL parameter (e.g., P=spa0021)
		itemNumberRegex := regexp.MustCompile(`[?&][SPMGBspgmb]=([^&]+)`)
		matches := itemNumberRegex.FindStringSubmatch(href)
		if len(matches) < 2 {
			return
		}

		item := &domain.CatalogPageItem{
			ItemNumber: matches[1],
		}

		if !strings.HasPrefix(href, "http") {
			href = p.baseURL + href
		}
		item.ItemURL = href

		if item.ItemNumber != "" {
			page.Items = append(page.Items, *item)
			itemCount++
		}
	})

	log.Debugf("Extracted %d items from page", itemCount)
	return page, nil
}

func (p *catalogParser) extractSubcategoryHierarchy(doc *goquery.Document) domain.SubcategoryHierarchy {
	var subcategories []domain.SubcategoryInfo
	var pathParts []string

	// Look for the specific breadcrumb pattern in table cells
	// The breadcrumb typically appears in a cell with background-color: #eeeeee
	doc.Find("td[style*='background-color: #eeeeee'], td[bgcolor='#eeeeee']").Each(func(i int, td *goquery.Selection) {
		text := strings.TrimSpace(td.Text())
		// Must start with "Catalog:" and contain the item number at the end
		if strings.HasPrefix(text, "Catalog:") && strings.Contains(text, ":") {
			// Extract only the breadcrumb links, not all links in the cell
			td.Find("a[href*='catalog.asp'], a[href*='catalogTree.asp'], a[href*='catalogList.asp']").Each(func(j int, link *goquery.Selection) {
				href, exists := link.Attr("href")
				name := strings.TrimSpace(link.Text())

				if !exists || name == "" || name == "Catalog" {
					return // Skip catalog root
				}

				// Only process actual category links, not action buttons
				if strings.Contains(href, "catalog.asp") ||
					strings.Contains(href, "catalogTree.asp") ||
					strings.Contains(href, "catalogList.asp") {

					// Extract ID from different URL patterns
					var id string
					if strings.Contains(href, "itemType=") {
						// Pattern: catalogTree.asp?itemType=B
						re := regexp.MustCompile(`itemType=([^&]+)`)
						if matches := re.FindStringSubmatch(href); len(matches) > 1 {
							id = matches[1]
						}
					} else if strings.Contains(href, "catString=") {
						// Pattern: catalogList.asp?catType=B&catString=332.124.316
						re := regexp.MustCompile(`catString=([^&]+)`)
						if matches := re.FindStringSubmatch(href); len(matches) > 1 {
							id = matches[1]
						}
					}

					// Ensure URL is absolute
					fullURL := href
					if strings.HasPrefix(href, "//") {
						fullURL = "https:" + href
					} else if strings.HasPrefix(href, "/") {
						fullURL = p.baseURL + href
					}

					subcategories = append(subcategories, domain.SubcategoryInfo{
						Name: name,
						URL:  fullURL,
						ID:   id,
					})
					pathParts = append(pathParts, name)
				}
			})
			return // Found the breadcrumb, stop searching
		}
	})

	return domain.SubcategoryHierarchy{
		FullPath:      strings.Join(pathParts, ": "),
		Subcategories: subcategories,
	}
}

func (p *catalogParser) ParseItemDetails(html, itemID string) (*domain.PartDetails, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// Extract item name from h1 heading
	name := strings.TrimSpace(doc.Find("h1").First().Text())

	// Extract detailed subcategory hierarchy from breadcrumbs
	subcategoryHierarchy := p.extractSubcategoryHierarchy(doc)

	// Build legacy category path for backward compatibility
	var categoryPath []string
	for _, subcat := range subcategoryHierarchy.Subcategories {
		categoryPath = append(categoryPath, subcat.Name)
	}

	// Parse item info using regex patterns
	var year, weight, dimensions string
	itemInfoText := doc.Find("strong:contains('Item Info')").Parent().Text()

	// Extract year
	if yearMatch := regexp.MustCompile(`Years? Released:\s*([0-9\-\s,]+)`).FindStringSubmatch(itemInfoText); len(yearMatch) > 1 {
		year = strings.TrimSpace(yearMatch[1])
	}

	// Extract weight
	if weightMatch := regexp.MustCompile(`Weight:\s*([0-9.]+g)`).FindStringSubmatch(itemInfoText); len(weightMatch) > 1 {
		weight = weightMatch[1]
	}

	// Extract dimensions - look for both Stud Dim. and Pack. Dim.
	var dimensionParts []string
	if studDimMatch := regexp.MustCompile(`Stud Dim\.:\s*([0-9x\s]+in\s+studs)`).FindStringSubmatch(itemInfoText); len(studDimMatch) > 1 {
		dimensionParts = append(dimensionParts, "Stud: "+strings.TrimSpace(studDimMatch[1]))
	}
	if packDimMatch := regexp.MustCompile(`Pack\. Dim\.:\s*([0-9.\s]+x[0-9.\s]+x[0-9.\s]+\s*cm\s*\([VH]?\))`).FindStringSubmatch(itemInfoText); len(packDimMatch) > 1 {
		dimensionParts = append(dimensionParts, "Pack: "+strings.TrimSpace(packDimMatch[1]))
	}
	dimensions = strings.Join(dimensionParts, "; ")

	// Extract colors - look specifically for color information, not all links
	var colors []domain.ColorInfo
	colorMap := make(map[string]domain.ColorInfo) // To avoid duplicates by color ID

	// Regex to extract colorID from URL
	colorIDRegex := regexp.MustCompile(`colorID=(\d+)`)

	// Method 1: Look for "Price Guide Info:" section (most comprehensive)
	doc.Find("tr").Each(func(i int, tr *goquery.Selection) {
		trText := strings.TrimSpace(tr.Text())
		if strings.Contains(trText, "Price Guide Info:") {
			tr.Find("a").Each(func(j int, a *goquery.Selection) {
				href, hasHref := a.Attr("href")
				colorText := strings.TrimSpace(a.Text())

				// Only consider links that look like color links and don't contain numbers
				if hasHref &&
					strings.Contains(href, "colorID=") && // Must be a color link
					colorText != "" &&
					!strings.Contains(colorText, "(") && // Skip entries with counts like "(122)"
					!strings.ContainsAny(colorText, "0123456789") && // Skip if contains numbers
					len(colorText) > 2 && // Skip very short entries
					colorText != "View All Colors" {

					// Extract color ID from href
					matches := colorIDRegex.FindStringSubmatch(href)
					if len(matches) > 1 {
						colorID := matches[1]
						colorMap[colorID] = domain.ColorInfo{
							ID:   colorID,
							Name: colorText,
						}
					}
				}
			})
		}
	})

	// Method 2: Look for "Lots For Sale:" if Price Guide didn't yield enough results
	if len(colorMap) < 5 { // If we have fewer than 5 colors, try another source
		doc.Find("tr").Each(func(i int, tr *goquery.Selection) {
			trText := strings.TrimSpace(tr.Text())
			if strings.Contains(trText, "Lots For Sale:") {
				tr.Find("a").Each(func(j int, a *goquery.Selection) {
					href, hasHref := a.Attr("href")
					colorText := strings.TrimSpace(a.Text())

					// Only consider links that look like color links and don't contain numbers
					if hasHref &&
						strings.Contains(href, "colorID=") && // Must be a color link
						colorText != "" &&
						!strings.Contains(colorText, "(") && // Skip entries with counts like "(122)"
						!strings.ContainsAny(colorText, "0123456789") && // Skip if contains numbers
						len(colorText) > 2 && // Skip very short entries
						colorText != "View All Colors" {

						// Extract color ID from href
						matches := colorIDRegex.FindStringSubmatch(href)
						if len(matches) > 1 {
							colorID := matches[1]
							// Only add if not already exists
							if _, exists := colorMap[colorID]; !exists {
								colorMap[colorID] = domain.ColorInfo{
									ID:   colorID,
									Name: colorText,
								}
							}
						}
					}
				})
			}
		})
	}

	// Method 3: Look for "Known Colors:" as additional source
	doc.Find("tr").Each(func(i int, tr *goquery.Selection) {
		trText := strings.TrimSpace(tr.Text())
		if strings.Contains(trText, "Known Colors:") {
			tr.Find("a").Each(func(j int, a *goquery.Selection) {
				href, hasHref := a.Attr("href")
				colorText := strings.TrimSpace(a.Text())

				// Only consider links that look like color links
				if hasHref &&
					strings.Contains(href, "colorID=") && // Must be a color link
					colorText != "" &&
					!strings.Contains(colorText, "(") && // Skip entries with counts
					!strings.ContainsAny(colorText, "0123456789") && // Skip if contains numbers
					len(colorText) > 2 {

					// Extract color ID from href
					matches := colorIDRegex.FindStringSubmatch(href)
					if len(matches) > 1 {
						colorID := matches[1]
						// Only add if not already exists
						if _, exists := colorMap[colorID]; !exists {
							colorMap[colorID] = domain.ColorInfo{
								ID:   colorID,
								Name: colorText,
							}
						}
					}
				}
			})
		}
	})

	// Convert map to slice
	for _, colorInfo := range colorMap {
		colors = append(colors, colorInfo)
	}

	// Find "Item Appears In" - look for the strong tag with this text, then find links in same cell
	var appearsIn []string
	doc.Find("strong").Each(func(i int, strong *goquery.Selection) {
		if strings.TrimSpace(strong.Text()) == "Item Appears In" {
			// Found the "Item Appears In" header, look for links in the same cell
			cell := strong.Parent()
			// Find links in this cell that point to catalogItemIn.asp with in=S parameter
			cell.Find("a").Each(func(j int, a *goquery.Selection) {
				href, exists := a.Attr("href")
				if exists && strings.Contains(href, "catalogItemIn.asp") && strings.Contains(href, "in=S") {
					if !strings.HasPrefix(href, "http") {
						href = p.baseURL + href
					}
					appearsIn = append(appearsIn, href)
				}
			})
		}
	})

	// Handle "Item Consists Of" - check for "N/A"
	var consistsOf []string
	doc.Find("strong").Each(func(i int, strong *goquery.Selection) {
		if strings.TrimSpace(strong.Text()) == "Item Consists Of" {
			// Found the "Item Consists Of" header, look for links in the same cell
			cell := strong.Parent()
			if strings.Contains(strings.TrimSpace(cell.Text()), "N/A") {
				// Leave consistsOf empty when N/A
				consistsOf = []string{}
			} else {
				// Look for actual links in this specific cell only
				cell.Find("a").Each(func(j int, a *goquery.Selection) {
					href, exists := a.Attr("href")
					if exists && (strings.Contains(href, "catalogItemInv.asp") || strings.Contains(href, "invSet.asp")) {
						if !strings.HasPrefix(href, "http") {
							href = p.baseURL + href
						}
						consistsOf = append(consistsOf, href)
					}
				})
			}
		}
	})

	// Get item image URLs
	var imageURLs []string
	doc.Find("img[class*='ItemImage'], img[src*='ItemImage']").Each(func(i int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			if !strings.HasPrefix(src, "http") {
				src = "https:" + src
			}
			imageURLs = append(imageURLs, src)
		}
	})

	return &domain.PartDetails{
		ItemNumber:           itemID,
		ItemName:             name,
		FullCategory:         strings.Join(categoryPath, ": "),
		YearReleased:         year,
		Weight:               weight,
		Dimensions:           dimensions,
		Colors:               colors,
		AppearsIn:            appearsIn,
		ConsistsOf:           consistsOf,
		SubcategoryHierarchy: subcategoryHierarchy,
		ItemImageURL: func() string {
			if len(imageURLs) > 0 {
				return imageURLs[0]
			}
			return ""
		}(),
	}, nil
}
