package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// ShowOrdersLists lists current active and closed orders
func ShowOrdersLists(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	var activeOrders []models.Order
	var closedOrders []models.Order
	var availableTables []models.Table

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Load open tables for table assignment in new order modal
		tRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, name, capacity FROM tables
			WHERE restaurant_id = $1 AND status = 'available'
			ORDER BY name
		`, activeRestID)
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var t models.Table
				t.RestaurantID = activeRestID
				if err := tRows.Scan(&t.ID, &t.Name, &t.Capacity); err == nil {
					availableTables = append(availableTables, t)
				}
			}
		}

		// Load open orders
		oRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT o.id, o.table_id, t.name, o.status, o.opened_at, o.subtotal, o.total_amount
			FROM orders o
			LEFT JOIN tables t ON t.id = o.table_id
			WHERE o.restaurant_id = $1 AND o.status = 'open'
			ORDER BY o.opened_at DESC
		`, activeRestID)
		if err == nil {
			defer oRows.Close()
			for oRows.Next() {
				var o models.Order
				var tabID sql.NullString
				var tabName sql.NullString
				if err := oRows.Scan(&o.ID, &tabID, &tabName, &o.Status, &o.OpenedAt, &o.Subtotal, &o.TotalAmount); err == nil {
					if tabID.Valid { o.TableID = &tabID.String }
					if tabName.Valid { o.TableName = tabName.String } else { o.TableName = "Takeaway" }
					activeOrders = append(activeOrders, o)
				}
			}
		}

		// Load recently closed/cancelled orders
		cRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT o.id, o.table_id, t.name, o.status, o.opened_at, o.closed_at, o.subtotal, o.total_amount
			FROM orders o
			LEFT JOIN tables t ON t.id = o.table_id
			WHERE o.restaurant_id = $1 AND o.status IN ('closed', 'cancelled')
			ORDER BY o.closed_at DESC LIMIT 20
		`, activeRestID)
		if err == nil {
			defer cRows.Close()
			for cRows.Next() {
				var o models.Order
				var tabID sql.NullString
				var tabName sql.NullString
				var closedAt time.Time
				if err := cRows.Scan(&o.ID, &tabID, &tabName, &o.Status, &o.OpenedAt, &closedAt, &o.Subtotal, &o.TotalAmount); err == nil {
					if tabID.Valid { o.TableID = &tabID.String }
					if tabName.Valid { o.TableName = tabName.String } else { o.TableName = "Takeaway" }
					o.ClosedAt = &closedAt
					closedOrders = append(closedOrders, o)
				}
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load orders: "+err.Error())
		return
	}

	c.HTML(http.StatusOK, "orders_list.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"ActiveNav":          "orders",
		"ActiveOrders":       activeOrders,
		"ClosedOrders":       closedOrders,
		"AvailableTables":    availableTables,
	})
}

// CreateOrder starts a new order against a table and updates table to occupied
func CreateOrder(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	tableID := c.PostForm("table_id")

	var orderID string

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var tableVal interface{}
		if tableID != "" {
			tableVal = tableID
			// Set table to occupied
			_, err := tx.ExecContext(c.Request.Context(), "UPDATE tables SET status = 'occupied' WHERE id = $1 AND restaurant_id = $2", tableID, activeRestID)
			if err != nil {
				return err
			}
		} else {
			tableVal = nil
		}

		err := tx.QueryRowContext(c.Request.Context(), `
			INSERT INTO orders (restaurant_id, table_id, status, opened_by, subtotal, tax_amount, discount_amount, total_amount)
			VALUES ($1, $2, 'open', $3, 0, 0, 0, 0)
			RETURNING id
		`, activeRestID, tableVal, user.ID).Scan(&orderID)
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to create order: "+err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/orders/"+orderID)
}

// ShowOrderDetails POS interface
func ShowOrderDetails(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	orderID := c.Param("id")

	var order models.Order
	var orderItems []models.OrderItem
	var categories []models.MenuCategory
	var menuItems []models.MenuItem

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Load Order details
		var tabID sql.NullString
		var tabName sql.NullString
		err := tx.QueryRowContext(c.Request.Context(), `
			SELECT o.id, o.table_id, t.name, o.status, o.subtotal, o.tax_amount, o.discount_amount, o.total_amount
			FROM orders o
			LEFT JOIN tables t ON t.id = o.table_id
			WHERE o.id = $1 AND o.restaurant_id = $2
		`, orderID, activeRestID).Scan(&order.ID, &tabID, &tabName, &order.Status, &order.Subtotal, &order.TaxAmount, &order.DiscountAmount, &order.TotalAmount)
		if err != nil {
			return err
		}
		if tabID.Valid { order.TableID = &tabID.String }
		if tabName.Valid { order.TableName = tabName.String } else { order.TableName = "Takeaway" }

		// Load Order Items
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT oi.id, oi.menu_item_id, mi.name, oi.quantity, oi.unit_price, oi.notes
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			WHERE oi.order_id = $1
		`, orderID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var oi models.OrderItem
				oi.OrderID = orderID
				var notes sql.NullString
				if err := rows.Scan(&oi.ID, &oi.MenuItemID, &oi.MenuItemName, &oi.Quantity, &oi.UnitPrice, &notes); err == nil {
					if notes.Valid { oi.Notes = &notes.String }
					orderItems = append(orderItems, oi)
				}
			}
		}

		// Load Menu details for display
		catRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, name FROM menu_categories WHERE restaurant_id = $1 AND deleted_at IS NULL ORDER BY sort_order, name
		`, activeRestID)
		if err == nil {
			defer catRows.Close()
			for catRows.Next() {
				var mc models.MenuCategory
				if err := catRows.Scan(&mc.ID, &mc.Name); err == nil {
					categories = append(categories, mc)
				}
			}
		}

		miRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, category_id, name, price FROM menu_items WHERE restaurant_id = $1 AND deleted_at IS NULL AND is_available = true ORDER BY name
		`, activeRestID)
		if err == nil {
			defer miRows.Close()
			for miRows.Next() {
				var mi models.MenuItem
				var catID sql.NullString
				if err := miRows.Scan(&mi.ID, &catID, &mi.Name, &mi.Price); err == nil {
					if catID.Valid { mi.CategoryID = &catID.String }
					menuItems = append(menuItems, mi)
				}
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusNotFound, "Order not found or access denied: "+err.Error())
		return
	}

	c.HTML(http.StatusOK, "orders_pos.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"ActiveNav":          "orders",
		"Order":              order,
		"OrderItems":         orderItems,
		"Categories":         categories,
		"MenuItems":          menuItems,
	})
}

// AddOrderItem adds/increments item in current order
func AddOrderItem(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	orderID := c.Param("id")
	itemID := c.PostForm("menu_item_id")

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Verify order belongs to active restaurant and is open
		var status string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT status FROM orders WHERE id = $1 AND restaurant_id = $2", orderID, activeRestID).Scan(&status)
		if err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("order is already closed")
		}

		// Fetch item details
		var itemPrice int
		err = tx.QueryRowContext(c.Request.Context(), "SELECT price FROM menu_items WHERE id = $1 AND restaurant_id = $2", itemID, activeRestID).Scan(&itemPrice)
		if err != nil {
			return err
		}

		// Check if item already in order
		var existingID string
		var existingQty int
		err = tx.QueryRowContext(c.Request.Context(), "SELECT id, quantity FROM order_items WHERE order_id = $1 AND menu_item_id = $2", orderID, itemID).
			Scan(&existingID, &existingQty)

		if err == nil {
			// Update quantity
			_, err = tx.ExecContext(c.Request.Context(), "UPDATE order_items SET quantity = quantity + 1 WHERE id = $1", existingID)
		} else if err == sql.ErrNoRows {
			// Insert new item
			_, err = tx.ExecContext(c.Request.Context(), `
				INSERT INTO order_items (order_id, menu_item_id, quantity, unit_price)
				VALUES ($1, $2, 1, $3)
			`, orderID, itemID, itemPrice)
		}

		if err != nil {
			return err
		}

		return RecalculateOrderTotals(c.Request.Context(), tx, orderID, activeRestID)
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	if c.GetHeader("HX-Request") == "true" {
		renderPOSOrderSidebar(c, orderID, activeRestID)
		return
	}

	c.Redirect(http.StatusSeeOther, "/orders/"+orderID)
}

// UpdateItemQty increments, decrements, or removes an order item
func UpdateItemQty(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	orderID := c.Param("id")
	orderItemID := c.Param("item_id")
	action := c.PostForm("action") // "inc", "dec", "remove"

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Verify ownership
		var orderStatus string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT status FROM orders WHERE id = $1 AND restaurant_id = $2", orderID, activeRestID).Scan(&orderStatus)
		if err != nil {
			return err
		}
		if orderStatus != "open" {
			return fmt.Errorf("order is closed")
		}

		var qty int
		err = tx.QueryRowContext(c.Request.Context(), "SELECT quantity FROM order_items WHERE id = $1 AND order_id = $2", orderItemID, orderID).Scan(&qty)
		if err != nil {
			return err
		}

		if action == "inc" {
			_, err = tx.ExecContext(c.Request.Context(), "UPDATE order_items SET quantity = quantity + 1 WHERE id = $1", orderItemID)
		} else if action == "dec" && qty > 1 {
			_, err = tx.ExecContext(c.Request.Context(), "UPDATE order_items SET quantity = quantity - 1 WHERE id = $1", orderItemID)
		} else {
			// delete item
			_, err = tx.ExecContext(c.Request.Context(), "DELETE FROM order_items WHERE id = $1", orderItemID)
		}

		if err != nil {
			return err
		}

		return RecalculateOrderTotals(c.Request.Context(), tx, orderID, activeRestID)
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	if c.GetHeader("HX-Request") == "true" {
		renderPOSOrderSidebar(c, orderID, activeRestID)
		return
	}

	c.Redirect(http.StatusSeeOther, "/orders/"+orderID)
}

// OverrideItemPrice adjusts unit price and logs audit adjust trails
func OverrideItemPrice(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	orderID := c.Param("id")
	orderItemID := c.Param("item_id")
	newPriceFloat, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	newPricePoisha := int(newPriceFloat * 100)
	reason := c.PostForm("reason")

	if reason == "" {
		c.String(http.StatusBadRequest, "Reason is required for manual price override audit")
		return
	}

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Fetch original unit_price
		var originalPrice int
		err := tx.QueryRowContext(c.Request.Context(), "SELECT unit_price FROM order_items WHERE id = $1 AND order_id = $2", orderItemID, orderID).Scan(&originalPrice)
		if err != nil {
			return err
		}

		// Update price
		_, err = tx.ExecContext(c.Request.Context(), "UPDATE order_items SET unit_price = $1 WHERE id = $2", newPricePoisha, orderItemID)
		if err != nil {
			return err
		}

		// Log audit override trail
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO order_item_price_adjustments (order_item_id, restaurant_id, original_price, adjusted_price, reason, adjusted_by)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, orderItemID, activeRestID, originalPrice, newPricePoisha, reason, user.ID)
		if err != nil {
			return err
		}

		return RecalculateOrderTotals(c.Request.Context(), tx, orderID, activeRestID)
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	if c.GetHeader("HX-Request") == "true" {
		renderPOSOrderSidebar(c, orderID, activeRestID)
		return
	}

	c.Redirect(http.StatusSeeOther, "/orders/"+orderID)
}

func renderPOSOrderSidebar(c *gin.Context, orderID string, activeRestID string) {
	var order models.Order
	var orderItems []models.OrderItem

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var tabID sql.NullString
		var tabName sql.NullString
		err := tx.QueryRowContext(c.Request.Context(), `
			SELECT o.id, o.table_id, t.name, o.status, o.subtotal, o.tax_amount, o.discount_amount, o.total_amount
			FROM orders o
			LEFT JOIN tables t ON t.id = o.table_id
			WHERE o.id = $1 AND o.restaurant_id = $2
		`, orderID, activeRestID).Scan(&order.ID, &tabID, &tabName, &order.Status, &order.Subtotal, &order.TaxAmount, &order.DiscountAmount, &order.TotalAmount)
		if err != nil {
			return err
		}
		if tabID.Valid {
			order.TableID = &tabID.String
		}
		if tabName.Valid {
			order.TableName = tabName.String
		} else {
			order.TableName = "Takeaway"
		}

		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT oi.id, oi.menu_item_id, mi.name, oi.quantity, oi.unit_price, oi.notes
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			WHERE oi.order_id = $1
			ORDER BY oi.id ASC
		`, orderID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var oi models.OrderItem
				oi.OrderID = orderID
				var notes sql.NullString
				if err := rows.Scan(&oi.ID, &oi.MenuItemID, &oi.MenuItemName, &oi.Quantity, &oi.UnitPrice, &notes); err == nil {
					if notes.Valid {
						oi.Notes = &notes.String
					}
					orderItems = append(orderItems, oi)
				}
			}
		}
		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	c.HTML(http.StatusOK, "pos_order_sidebar", gin.H{
		"Order":      order,
		"OrderItems": orderItems,
	})
}

// CloseOrder payment collection, invoices billing counters, and table release
func CloseOrder(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	orderID := c.Param("id")
	paymentMethod := c.PostForm("payment_method")

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Fetch order details
		var tableID sql.NullString
		var subtotal, discount, total int
		var status string
		err := tx.QueryRowContext(c.Request.Context(), `
			SELECT table_id, subtotal, discount_amount, total_amount, status 
			FROM orders WHERE id = $1 AND restaurant_id = $2
		`, orderID, activeRestID).Scan(&tableID, &subtotal, &discount, &total, &status)
		if err != nil {
			return err
		}

		if status != "open" {
			return fmt.Errorf("order is already closed")
		}

		// Get tax configurations
		var vatRateBps int
		var vatInclusive bool
		var serviceChargeRateBps int
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT vat_rate_bps, vat_inclusive, service_charge_rate_bps 
			FROM restaurant_tax_settings WHERE restaurant_id = $1
		`, activeRestID).Scan(&vatRateBps, &vatInclusive, &serviceChargeRateBps)
		if err == sql.ErrNoRows {
			// default to standard 15% VAT inclusive
			vatRateBps = 1500
			vatInclusive = true
			serviceChargeRateBps = 0
		} else if err != nil {
			return err
		}

		// Re-run tax calculation explicitly
		var taxAmount int
		var totalAmount int

		serviceCharge := (subtotal * serviceChargeRateBps) / 10000

		if vatInclusive {
			// Subtotal includes VAT. Find the net amount and extract VAT
			netRevenue := (subtotal * 10000) / (10000 + vatRateBps)
			taxAmount = subtotal - netRevenue
			totalAmount = subtotal - discount + serviceCharge
		} else {
			// Subtotal is net. Calculate tax on top
			taxAmount = (subtotal * vatRateBps) / 10000
			totalAmount = subtotal + taxAmount - discount + serviceCharge
		}

		// Close Order
		_, err = tx.ExecContext(c.Request.Context(), `
			UPDATE orders 
			SET status = 'closed', closed_at = $1, tax_amount = $2, total_amount = $3
			WHERE id = $4
		`, time.Now(), taxAmount, totalAmount, orderID)
		if err != nil {
			return err
		}

		// Record payment method
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO order_payments (order_id, restaurant_id, method, amount, received_by)
			VALUES ($1, $2, $3, $4, $5)
		`, orderID, activeRestID, paymentMethod, totalAmount, user.ID)
		if err != nil {
			return err
		}

		// Update sequential invoice counter for restaurant
		var nextInvNumber int
		err = tx.QueryRowContext(c.Request.Context(), `
			INSERT INTO restaurant_invoice_counters (restaurant_id, last_number)
			VALUES ($1, 1)
			ON CONFLICT (restaurant_id)
			DO UPDATE SET last_number = restaurant_invoice_counters.last_number + 1
			RETURNING last_number
		`, activeRestID).Scan(&nextInvNumber)
		if err != nil {
			return err
		}

		// Create invoice
		mockPDF := fmt.Sprintf("/invoices/%s/pdf", orderID)
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO invoices (restaurant_id, order_id, invoice_number, subtotal, tax_amount, discount_amount, total_amount, pdf_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, activeRestID, orderID, nextInvNumber, subtotal, taxAmount, discount, totalAmount, mockPDF)
		if err != nil {
			return err
		}

		// Release dining table
		if tableID.Valid {
			_, err = tx.ExecContext(c.Request.Context(), "UPDATE tables SET status = 'available' WHERE id = $1", tableID.String)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to close order: "+err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/orders")
}

// RecalculateOrderTotals aggregates order_items prices to orders summary
func RecalculateOrderTotals(ctx context.Context, tx *sql.Tx, orderID string, restaurantID string) error {
	var subtotal int
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(quantity * unit_price), 0) FROM order_items WHERE order_id = $1", orderID).Scan(&subtotal)
	if err != nil {
		return err
	}

	// Fetch tax rates
	var vatRateBps int
	var vatInclusive bool
	var serviceChargeRateBps int
	err = tx.QueryRowContext(ctx, `
		SELECT vat_rate_bps, vat_inclusive, service_charge_rate_bps 
		FROM restaurant_tax_settings WHERE restaurant_id = $1
	`, restaurantID).Scan(&vatRateBps, &vatInclusive, &serviceChargeRateBps)
	if err == sql.ErrNoRows {
		vatRateBps = 1500
		vatInclusive = true
		serviceChargeRateBps = 0
	} else if err != nil {
		return err
	}

	serviceCharge := (subtotal * serviceChargeRateBps) / 10000
	var taxAmount int
	var totalAmount int

	if vatInclusive {
		netRevenue := (subtotal * 10000) / (10000 + vatRateBps)
		taxAmount = subtotal - netRevenue
		totalAmount = subtotal + serviceCharge
	} else {
		taxAmount = (subtotal * vatRateBps) / 10000
		totalAmount = subtotal + taxAmount + serviceCharge
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE orders 
		SET subtotal = $1, tax_amount = $2, total_amount = $3
		WHERE id = $4
	`, subtotal, taxAmount, totalAmount, orderID)
	return err
}

// ShowMockInvoice renders a clean PDF-like HTML invoice receipt
func ShowMockInvoice(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	orderID := c.Param("id")

	var order models.Order
	var invoice models.Invoice
	var items []models.OrderItem
	var restName, restAddress, restPhone string

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Restaurant details
		var addr, phone sql.NullString
		err := tx.QueryRowContext(c.Request.Context(), "SELECT name, address, phone FROM restaurants WHERE id = $1", activeRestID).Scan(&restName, &addr, &phone)
		if err != nil {
			return err
		}
		if addr.Valid { restAddress = addr.String }
		if phone.Valid { restPhone = phone.String }

		// Invoice details
		var pdf sql.NullString
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT id, invoice_number, subtotal, tax_amount, discount_amount, total_amount, pdf_url, created_at
			FROM invoices WHERE order_id = $1 AND restaurant_id = $2
		`, orderID, activeRestID).Scan(&invoice.ID, &invoice.InvoiceNumber, &invoice.Subtotal, &invoice.TaxAmount, &invoice.DiscountAmount, &invoice.TotalAmount, &pdf, &invoice.CreatedAt)
		if err != nil {
			return err
		}
		if pdf.Valid { invoice.PdfURL = &pdf.String }

		// Order table context
		var tabName sql.NullString
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT t.name FROM orders o
			LEFT JOIN tables t ON t.id = o.table_id
			WHERE o.id = $1
		`, orderID).Scan(&tabName)
		if err == nil && tabName.Valid {
			order.TableName = tabName.String
		} else {
			order.TableName = "Takeaway"
		}

		// Items list
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT oi.quantity, oi.unit_price, mi.name
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			WHERE oi.order_id = $1
		`, orderID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var oi models.OrderItem
				if err := rows.Scan(&oi.Quantity, &oi.UnitPrice, &oi.MenuItemName); err == nil {
					items = append(items, oi)
				}
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusNotFound, "Invoice not found: "+err.Error())
		return
	}

	c.HTML(http.StatusOK, "invoice_receipt.tmpl", gin.H{
		"RestaurantName":    restName,
		"RestaurantAddress": restAddress,
		"RestaurantPhone":   restPhone,
		"Invoice":           invoice,
		"TableName":         order.TableName,
		"Items":             items,
	})
}
