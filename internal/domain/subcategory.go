package domain

// SubcategoryInfo represents a single subcategory in the catalog breadcrumb
type SubcategoryInfo struct {
	Name string `json:"name"` // Display name like "Books", "Informational Book", "Train"
	URL  string `json:"url"`  // Full URL to the subcategory page
	ID   string `json:"id"`   // Category ID extracted from URL (e.g., "B", "332", "332.124")
}

// SubcategoryHierarchy represents the full breadcrumb path for an item
type SubcategoryHierarchy struct {
	FullPath      string            `json:"full_path"`     // Complete path as string "Books: Informational Book: Train: 4.5V"
	Subcategories []SubcategoryInfo `json:"subcategories"` // Individual subcategory details
}
