package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"math"
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
		// Include occupied tables so an existing table session can be resumed.
		tRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, name, capacity, status FROM tables
			WHERE restaurant_id = $1
			ORDER BY name
			`, activeRestID)
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var t models.Table
				t.RestaurantID = activeRestID
				if err := tRows.Scan(&t.ID, &t.Name, &t.Capacity, &t.Status); err == nil {
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
					if tabID.Valid {
						o.TableID = &tabID.String
					}
					if tabName.Valid {
						o.TableName = tabName.String
					} else {
						o.TableName = "Takeaway"
					}
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
					if tabID.Valid {
						o.TableID = &tabID.String
					}
					if tabName.Valid {
						o.TableName = tabName.String
					} else {
						o.TableName = "Takeaway"
					}
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
			// Serialize order creation for this table before checking for an open order.
			var lockedTableID string
			if err := tx.QueryRowContext(c.Request.Context(), `
				SELECT id FROM tables WHERE id = $1 AND restaurant_id = $2 FOR UPDATE
			`, tableID, activeRestID).Scan(&lockedTableID); err != nil {
				return err
			}

			err := tx.QueryRowContext(c.Request.Context(), `
				SELECT id FROM orders
				WHERE restaurant_id = $1 AND table_id = $2 AND status = 'open'
				ORDER BY opened_at ASC LIMIT 1
			`, activeRestID, tableID).Scan(&orderID)
			if err == nil {
				_, err = tx.ExecContext(c.Request.Context(), "UPDATE tables SET status = 'occupied' WHERE id = $1", tableID)
				return err
			}
			if err != sql.ErrNoRows {
				return err
			}

			if _, err := tx.ExecContext(c.Request.Context(), "UPDATE tables SET status = 'occupied' WHERE id = $1", tableID); err != nil {
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
	var staff []models.RestaurantStaff

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Load Order details
		var tabID, tabName, staffID, staffName, discountType sql.NullString
		var discountValue sql.NullInt64
		err := tx.QueryRowContext(c.Request.Context(), `
			SELECT o.id, o.table_id, t.name, o.status, o.opened_at, o.subtotal, o.tax_amount,
			       o.discount_amount, o.total_amount, o.staff_id, rs.name, o.discount_type, o.discount_value
			FROM orders o
			LEFT JOIN tables t ON t.id = o.table_id
			LEFT JOIN restaurant_staff rs ON rs.id = o.staff_id
			WHERE o.id = $1 AND o.restaurant_id = $2
		`, orderID, activeRestID).Scan(&order.ID, &tabID, &tabName, &order.Status, &order.OpenedAt, &order.Subtotal, &order.TaxAmount, &order.DiscountAmount, &order.TotalAmount, &staffID, &staffName, &discountType, &discountValue)
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
		if staffID.Valid {
			order.StaffID = &staffID.String
		}
		if staffName.Valid {
			order.StaffName = staffName.String
		}
		if discountType.Valid {
			order.DiscountType = &discountType.String
		}
		if discountValue.Valid {
			value := int(discountValue.Int64)
			order.DiscountValue = &value
		}

		// Load Order Items
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT oi.id, oi.menu_item_id, mi.name, oi.quantity, oi.unit_price,
			       oi.discount_type, oi.discount_value, oi.discount_amount, oi.notes
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			WHERE oi.order_id = $1
		`, orderID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var oi models.OrderItem
				oi.OrderID = orderID
				var notes, discountType sql.NullString
				var discountValue sql.NullInt64
				if err := rows.Scan(&oi.ID, &oi.MenuItemID, &oi.MenuItemName, &oi.Quantity, &oi.UnitPrice, &discountType, &discountValue, &oi.DiscountAmount, &notes); err == nil {
					if notes.Valid {
						oi.Notes = &notes.String
					}
					if discountType.Valid {
						oi.DiscountType = &discountType.String
					}
					if discountValue.Valid {
						value := int(discountValue.Int64)
						oi.DiscountValue = &value
					}
					orderItems = append(orderItems, oi)
				}
			}
		}

		staffRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, restaurant_id, name, is_waiter, is_cashier, custom_title, is_active
			FROM restaurant_staff WHERE restaurant_id = $1 AND is_active = true ORDER BY name
		`, activeRestID)
		if err != nil {
			return err
		}
		defer staffRows.Close()
		for staffRows.Next() {
			var member models.RestaurantStaff
			var title sql.NullString
			if err := staffRows.Scan(&member.ID, &member.RestaurantID, &member.Name, &member.IsWaiter, &member.IsCashier, &title, &member.IsActive); err != nil {
				return err
			}
			if title.Valid {
				member.CustomTitle = &title.String
			}
			staff = append(staff, member)
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
					if catID.Valid {
						mi.CategoryID = &catID.String
					}
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
		"Staff":              staff,
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

// OverrideItemPrice applies a fixed or percentage discount to an order line.
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
	discountType, discountValue, err := parseDiscount(c.PostForm("mode"), c.PostForm("value"))
	if err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	reason := c.PostForm("reason")

	if reason == "" {
		c.String(http.StatusBadRequest, "Reason is required for manual price override audit")
		return
	}

	err = db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var originalPrice, quantity int
		var status string
		err := tx.QueryRowContext(c.Request.Context(), `
			SELECT oi.unit_price, oi.quantity, o.status FROM order_items oi
			JOIN orders o ON o.id = oi.order_id
			WHERE oi.id = $1 AND oi.order_id = $2 AND o.restaurant_id = $3
		`, orderItemID, orderID, activeRestID).Scan(&originalPrice, &quantity, &status)
		if err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("order is closed")
		}
		if discountType == "percent" && discountValue > 10000 {
			return fmt.Errorf("percentage discount cannot exceed 100%%")
		}
		lineAmount := quantity * originalPrice
		lineDiscount := calculateDiscount(lineAmount, discountType, discountValue)
		effectiveUnitPrice := (lineAmount - lineDiscount + quantity/2) / quantity
		_, err = tx.ExecContext(c.Request.Context(), `
			UPDATE order_items SET discount_type = $1, discount_value = $2, discount_amount = $3 WHERE id = $4
		`, discountType, discountValue, lineDiscount, orderItemID)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO order_item_price_adjustments (order_item_id, restaurant_id, original_price, adjusted_price, reason, adjusted_by, discount_type, discount_value)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, orderItemID, activeRestID, originalPrice, effectiveUnitPrice, reason, user.ID, discountType, discountValue)
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

func parseDiscount(mode, valueText string) (string, int, error) {
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return "", 0, fmt.Errorf("enter a valid non-negative discount")
	}
	if mode == "percent" {
		if value > 100 {
			return "", 0, fmt.Errorf("percentage discount cannot exceed 100%%")
		}
		return mode, int(math.Round(value * 100)), nil
	}
	if mode != "amount" {
		return "", 0, fmt.Errorf("discount must be an amount or percentage")
	}
	if value > float64(math.MaxInt32)/100 {
		return "", 0, fmt.Errorf("discount amount is too large")
	}
	return mode, int(math.Round(value * 100)), nil
}

func calculateDiscount(base int, mode string, value int) int {
	if base <= 0 || value <= 0 {
		return 0
	}
	if mode == "percent" {
		value = (base*value + 5000) / 10000
	}
	if value > base {
		return base
	}
	return value
}

func SetOrderStaff(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := value.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)
	orderID := c.Param("id")
	staffID := c.PostForm("staff_id")
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(c.Request.Context(), "SELECT status FROM orders WHERE id = $1 AND restaurant_id = $2", orderID, activeRestID).Scan(&status); err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("order is closed")
		}
		if staffID != "" {
			var active bool
			if err := tx.QueryRowContext(c.Request.Context(), "SELECT is_active FROM restaurant_staff WHERE id = $1 AND restaurant_id = $2", staffID, activeRestID).Scan(&active); err != nil {
				return err
			}
			if !active {
				return fmt.Errorf("staff member is inactive")
			}
			_, err := tx.ExecContext(c.Request.Context(), "UPDATE orders SET staff_id = $1 WHERE id = $2 AND restaurant_id = $3", staffID, orderID, activeRestID)
			return err
		}
		_, err := tx.ExecContext(c.Request.Context(), "UPDATE orders SET staff_id = NULL WHERE id = $1 AND restaurant_id = $2", orderID, activeRestID)
		return err
	})
	if err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	if c.GetHeader("HX-Request") == "true" {
		renderPOSOrderSidebar(c, orderID, activeRestID)
		return
	}
	c.Redirect(http.StatusSeeOther, "/orders/"+orderID)
}

func SetOrderDiscount(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := value.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)
	orderID := c.Param("id")
	discountType, discountValue, err := parseDiscount(c.PostForm("mode"), c.PostForm("value"))
	reason := c.PostForm("reason")
	clearing := c.PostForm("clear") == "true"
	if clearing {
		discountType, discountValue, err = "", 0, nil
		reason = "Order discount cleared"
	}
	if err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	if reason == "" {
		c.String(http.StatusBadRequest, "Reason is required for an order discount")
		return
	}
	err = db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(c.Request.Context(), "SELECT status FROM orders WHERE id = $1 AND restaurant_id = $2", orderID, activeRestID).Scan(&status); err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("order is closed")
		}
		if discountType == "percent" && discountValue > 10000 {
			return fmt.Errorf("percentage discount cannot exceed 100%%")
		}
		if clearing {
			if _, err := tx.ExecContext(c.Request.Context(), "UPDATE orders SET discount_type = NULL, discount_value = NULL WHERE id = $1", orderID); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(c.Request.Context(), "UPDATE orders SET discount_type = $1, discount_value = $2 WHERE id = $3", discountType, discountValue, orderID); err != nil {
			return err
		}
		if err := RecalculateOrderTotals(c.Request.Context(), tx, orderID, activeRestID); err != nil {
			return err
		}
		var discountAmount int
		if err := tx.QueryRowContext(c.Request.Context(), `
			SELECT discount_amount - COALESCE((SELECT SUM(discount_amount) FROM order_items WHERE order_id = $1), 0)
			FROM orders WHERE id = $1
		`, orderID).Scan(&discountAmount); err != nil {
			return err
		}
		auditType, auditValue := discountType, discountValue
		if clearing {
			auditType, auditValue = "cleared", 0
		}
		_, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO order_discount_adjustments (order_id, restaurant_id, discount_type, discount_value, discount_amount, reason, adjusted_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, orderID, activeRestID, auditType, auditValue, discountAmount, reason, user.ID)
		return err
	})
	if err != nil {
		c.String(http.StatusBadRequest, err.Error())
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
	var staff []models.RestaurantStaff

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var tabID, tabName, staffID, staffName, discountType sql.NullString
		var discountValue sql.NullInt64
		err := tx.QueryRowContext(c.Request.Context(), `
			SELECT o.id, o.table_id, t.name, o.status, o.opened_at, o.subtotal, o.tax_amount,
			       o.discount_amount, o.total_amount, o.staff_id, rs.name, o.discount_type, o.discount_value
			FROM orders o
			LEFT JOIN tables t ON t.id = o.table_id
			LEFT JOIN restaurant_staff rs ON rs.id = o.staff_id
			WHERE o.id = $1 AND o.restaurant_id = $2
		`, orderID, activeRestID).Scan(&order.ID, &tabID, &tabName, &order.Status, &order.OpenedAt, &order.Subtotal, &order.TaxAmount, &order.DiscountAmount, &order.TotalAmount, &staffID, &staffName, &discountType, &discountValue)
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
		if staffID.Valid {
			order.StaffID = &staffID.String
		}
		if staffName.Valid {
			order.StaffName = staffName.String
		}
		if discountType.Valid {
			order.DiscountType = &discountType.String
		}
		if discountValue.Valid {
			value := int(discountValue.Int64)
			order.DiscountValue = &value
		}

		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT oi.id, oi.menu_item_id, mi.name, oi.quantity, oi.unit_price,
			       oi.discount_type, oi.discount_value, oi.discount_amount, oi.notes
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
				var notes, discountType sql.NullString
				var discountValue sql.NullInt64
				if err := rows.Scan(&oi.ID, &oi.MenuItemID, &oi.MenuItemName, &oi.Quantity, &oi.UnitPrice, &discountType, &discountValue, &oi.DiscountAmount, &notes); err == nil {
					if notes.Valid {
						oi.Notes = &notes.String
					}
					if discountType.Valid {
						oi.DiscountType = &discountType.String
					}
					if discountValue.Valid {
						value := int(discountValue.Int64)
						oi.DiscountValue = &value
					}
					orderItems = append(orderItems, oi)
				}
			}
		}
		staffRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, restaurant_id, name, is_waiter, is_cashier, custom_title, is_active
			FROM restaurant_staff WHERE restaurant_id = $1 AND is_active = true ORDER BY name
		`, activeRestID)
		if err != nil {
			return err
		}
		defer staffRows.Close()
		for staffRows.Next() {
			var member models.RestaurantStaff
			var title sql.NullString
			if err := staffRows.Scan(&member.ID, &member.RestaurantID, &member.Name, &member.IsWaiter, &member.IsCashier, &title, &member.IsActive); err != nil {
				return err
			}
			if title.Valid {
				member.CustomTitle = &title.String
			}
			staff = append(staff, member)
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
		"Staff":      staff,
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
		if err := RecalculateOrderTotals(c.Request.Context(), tx, orderID, activeRestID); err != nil {
			return err
		}
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

		taxableSubtotal := subtotal - discount
		serviceCharge := (taxableSubtotal * serviceChargeRateBps) / 10000

		if vatInclusive {
			// Subtotal includes VAT. Find the net amount and extract VAT
			netRevenue := (taxableSubtotal * 10000) / (10000 + vatRateBps)
			taxAmount = taxableSubtotal - netRevenue
			totalAmount = taxableSubtotal + serviceCharge
		} else {
			// Subtotal is net. Calculate tax on top
			taxAmount = (taxableSubtotal * vatRateBps) / 10000
			totalAmount = taxableSubtotal + taxAmount + serviceCharge
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
			_, err = tx.ExecContext(c.Request.Context(), `
				UPDATE tables SET status = 'available'
				WHERE id = $1 AND restaurant_id = $2
				AND NOT EXISTS (SELECT 1 FROM orders WHERE table_id = $1 AND restaurant_id = $2 AND status = 'open')
			`, tableID.String, activeRestID)
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
	_, err := tx.ExecContext(ctx, `
		UPDATE order_items
		SET discount_amount = CASE
			WHEN discount_type = 'amount' THEN LEAST(quantity * unit_price, discount_value)
			WHEN discount_type = 'percent' THEN LEAST(quantity * unit_price, (quantity * unit_price * discount_value + 5000) / 10000)
			ELSE 0
		END
		WHERE order_id = $1
	`, orderID)
	if err != nil {
		return err
	}
	var subtotal, lineDiscount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity * unit_price), 0), COALESCE(SUM(discount_amount), 0)
		FROM order_items WHERE order_id = $1
	`, orderID).Scan(&subtotal, &lineDiscount); err != nil {
		return err
	}
	var discountType sql.NullString
	var discountValue sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT discount_type, discount_value FROM orders
		WHERE id = $1 AND restaurant_id = $2
	`, orderID, restaurantID).Scan(&discountType, &discountValue); err != nil {
		return err
	}
	orderDiscount := 0
	if discountType.Valid && discountValue.Valid {
		orderDiscount = calculateDiscount(subtotal-lineDiscount, discountType.String, int(discountValue.Int64))
	}
	discount := lineDiscount + orderDiscount
	taxableSubtotal := subtotal - discount

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

	serviceCharge := (taxableSubtotal * serviceChargeRateBps) / 10000
	var taxAmount int
	var totalAmount int

	if vatInclusive {
		netRevenue := (taxableSubtotal * 10000) / (10000 + vatRateBps)
		taxAmount = taxableSubtotal - netRevenue
		totalAmount = taxableSubtotal + serviceCharge
	} else {
		taxAmount = (taxableSubtotal * vatRateBps) / 10000
		totalAmount = taxableSubtotal + taxAmount + serviceCharge
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE orders 
		SET subtotal = $1, discount_amount = $2, tax_amount = $3, total_amount = $4
		WHERE id = $5 AND restaurant_id = $6
	`, subtotal, discount, taxAmount, totalAmount, orderID, restaurantID)
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
		if addr.Valid {
			restAddress = addr.String
		}
		if phone.Valid {
			restPhone = phone.String
		}

		// Invoice details
		var pdf sql.NullString
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT id, invoice_number, subtotal, tax_amount, discount_amount, total_amount, pdf_url, created_at
			FROM invoices WHERE order_id = $1 AND restaurant_id = $2
		`, orderID, activeRestID).Scan(&invoice.ID, &invoice.InvoiceNumber, &invoice.Subtotal, &invoice.TaxAmount, &invoice.DiscountAmount, &invoice.TotalAmount, &pdf, &invoice.CreatedAt)
		if err != nil {
			return err
		}
		if pdf.Valid {
			invoice.PdfURL = &pdf.String
		}

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
