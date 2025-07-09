package domain

type CatalogPageItem struct {
	ItemNumber string `json:"item_number"`
	ItemURL    string `json:"item_url"`
}

type CatalogPage struct {
	PageNumber   int               `json:"page_number"`    // Current page number
	TotalPages   int               `json:"total_pages"`    // Total number of pages
	TotalItems   int               `json:"total_items"`    // Total items found
	ItemsPerPage int               `json:"items_per_page"` // Items shown per page
	CategoryType CategoryType      `json:"category_type"`  // S, P, M, G, B
	Items        []CatalogPageItem `json:"items"`          // Items on this page
}

type CatalogResults struct {
	CategoryType CategoryType   `json:"category_type"` // S, P, M, G, B
	CategoryLike string         `json:"category_like"` // Filter parameter
	TotalItems   int            `json:"total_items"`   // Total items across all pages
	TotalPages   int            `json:"total_pages"`   // Total pages
	Pages        []*CatalogPage `json:"pages"`         // All pages with their items
}
