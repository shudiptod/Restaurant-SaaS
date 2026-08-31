package models

import "time"

// RestaurantUserContext defines the active restaurant membership context for the logged-in user
type RestaurantUserContext struct {
	RestaurantID   string `json:"restaurant_id"`
	RestaurantName string `json:"restaurant_name"`
	Role           string `json:"role"`
}

// User represents platform user
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Phone        *string   `json:"phone"`
	PasswordHash string    `json:"-"`
	FullName     string    `json:"full_name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Restaurant represents a tenant restaurant
type Restaurant struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"account_id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Status       string    `json:"status"`
	LockedReason *string   `json:"locked_reason"`
	Timezone     string    `json:"timezone"`
	Address      *string   `json:"address"`
	Phone        *string   `json:"phone"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Table represents a dining table inside a restaurant
type Table struct {
	ID           string `json:"id"`
	RestaurantID string `json:"restaurant_id"`
	Name         string `json:"name"`
	Capacity     *int   `json:"capacity"`
	Status       string `json:"status"` // available, occupied, reserved
}

// MenuCategory represents a category of menu items
type MenuCategory struct {
	ID           string     `json:"id"`
	RestaurantID string     `json:"restaurant_id"`
	Name         string     `json:"name"`
	SortOrder    int        `json:"sort_order"`
	DeletedAt    *time.Time `json:"deleted_at"`
}

// MenuItem represents an item on the menu
type MenuItem struct {
	ID           string     `json:"id"`
	RestaurantID string     `json:"restaurant_id"`
	CategoryID   *string    `json:"category_id"`
	Name         string     `json:"name"`
	Description  *string    `json:"description"`
	Price        int        `json:"price"` // in poisha
	IsAvailable  bool       `json:"is_available"`
	DeletedAt    *time.Time `json:"deleted_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Order represents a customer order
type Order struct {
	ID             string     `json:"id"`
	RestaurantID   string     `json:"restaurant_id"`
	TableID        *string    `json:"table_id"`
	TableName      string     `json:"table_name"` // Joined table name for UI convenience
	Status         string     `json:"status"`     // open, closed, cancelled
	OpenedBy       *string    `json:"opened_by"`
	OpenedByName   string     `json:"opened_by_name"`
	OpenedAt       time.Time  `json:"opened_at"`
	ClosedAt       *time.Time `json:"closed_at"`
	Subtotal       int        `json:"subtotal"` // poisha
	TaxAmount      int        `json:"tax_amount"`
	DiscountAmount int        `json:"discount_amount"`
	TotalAmount    int        `json:"total_amount"`
}

// OrderItem represents a line item in an order
type OrderItem struct {
	ID           string  `json:"id"`
	OrderID      string  `json:"order_id"`
	MenuItemID   string  `json:"menu_item_id"`
	MenuItemName string  `json:"menu_item_name"` // Joined menu item name
	Quantity     int     `json:"quantity"`
	UnitPrice    int     `json:"unit_price"` // poisha (actual price charged)
	Notes        *string `json:"notes"`
}

// OrderPayment records customer payment methods for split payments or simple checkout
type OrderPayment struct {
	ID           string    `json:"id"`
	OrderID      string    `json:"order_id"`
	RestaurantID string    `json:"restaurant_id"`
	Method       string    `json:"method"` // cash, card, bkash_personal, nagad, rocket, bank_transfer, other
	Amount       int       `json:"amount"` // poisha
	ReceivedBy   *string   `json:"received_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// Invoice represents a generated bill
type Invoice struct {
	ID             string    `json:"id"`
	RestaurantID   string    `json:"restaurant_id"`
	OrderID        string    `json:"order_id"`
	InvoiceNumber  int       `json:"invoice_number"`
	Subtotal       int       `json:"subtotal"`
	TaxAmount      int       `json:"tax_amount"`
	DiscountAmount int       `json:"discount_amount"`
	TotalAmount    int       `json:"total_amount"`
	PdfURL         *string   `json:"pdf_url"`
	CreatedAt      time.Time `json:"created_at"`
}

// InventoryItem represents an open inventory tracking item
type InventoryItem struct {
	ID               string     `json:"id"`
	RestaurantID     string     `json:"restaurant_id"`
	Name             string     `json:"name"`
	Unit             string     `json:"unit"`
	CurrentQuantity  float64    `json:"current_quantity"`
	ReorderThreshold *float64   `json:"reorder_threshold"`
	UnitCost         *int       `json:"unit_cost"` // in poisha
	DeletedAt        *time.Time `json:"deleted_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// InventoryAdjustment represents an immutable audit log entry for inventory movements
type InventoryAdjustment struct {
	ID              string    `json:"id"`
	InventoryItemID string    `json:"inventory_item_id"`
	ItemName        string    `json:"item_name"`
	Unit            string    `json:"unit"`
	RestaurantID    string    `json:"restaurant_id"`
	ChangeQuantity  float64   `json:"change_quantity"` // positive for stock-in, negative for stock-out
	Reason          string    `json:"reason"`          // purchase, usage, wastage, correction, other
	Note            string    `json:"note"`
	AdjustedBy      *string   `json:"adjusted_by"`
	AdjustedByName  string    `json:"adjusted_by_name"`
	CreatedAt       time.Time `json:"created_at"`
}

// RestaurantTaxSettings represents VAT / Mushak configurations per restaurant
type RestaurantTaxSettings struct {
	RestaurantID          string    `json:"restaurant_id"`
	VATRegistrationNumber *string   `json:"vat_registration_number"`
	VATRateBps            int       `json:"vat_rate_bps"` // basis points (1500 = 15%)
	VATInclusive          bool      `json:"vat_inclusive"`
	ServiceChargeRateBps  int       `json:"service_charge_rate_bps"`
	UpdatedAt             time.Time `json:"updated_at"`
}

