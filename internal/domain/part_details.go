package domain

type ColorInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PartDetails struct {
	ItemNumber           string               `json:"item_number"`
	YearReleased         string               `json:"year_released,omitempty"`
	Weight               string               `json:"weight,omitempty"`
	Dimensions           string               `json:"dimensions,omitempty"`
	AppearsIn            []string             `json:"appears_in,omitempty"`
	ConsistsOf           []string             `json:"consists_of,omitempty"`
	Colors               []ColorInfo          `json:"colors,omitempty"`
	FullCategory         string               `json:"full_category,omitempty"`
	ItemImageURL         string               `json:"item_image_url,omitempty"`
	ItemName             string               `json:"item_name,omitempty"`
	AlternateItem        string               `json:"alternate_item,omitempty"`
	SubcategoryHierarchy SubcategoryHierarchy `json:"subcategory_hierarchy,omitempty"`
}
