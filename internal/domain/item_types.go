package domain

type CategoryType string

func (c CategoryType) String() string {
	return string(c)
}

const (
	CategoryTypeSet     CategoryType = "S" // Sets
	CategoryTypePart    CategoryType = "P" // Parts
	CategoryTypeMinifig CategoryType = "M" // Minifigures
	CategoryTypeGear    CategoryType = "G" // Gear
	CategoryTypeBook    CategoryType = "B" // Books
)

var CategoryTypes = []CategoryType{
	CategoryTypeSet,
	CategoryTypePart,
	CategoryTypeMinifig,
	CategoryTypeGear,
	CategoryTypeBook,
}

func (c CategoryType) GetCategoryName() string {
	switch c {
	case CategoryTypeSet:
		return "Sets"
	case CategoryTypePart:
		return "Parts"
	case CategoryTypeMinifig:
		return "Minifigures"
	case CategoryTypeGear:
		return "Gear"
	case CategoryTypeBook:
		return "Books"
	default:
		return "Unknown"
	}
}
