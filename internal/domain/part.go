package domain

type Part struct {
	ID string `json:"id"`
}

type CategoryInfo struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Count      int    `json:"count"`
	CategoryID string `json:"category_id"`
	CatTmpID   string `json:"cat_tmp_id,omitempty"`
	ItemType   string `json:"item_type"`
}
