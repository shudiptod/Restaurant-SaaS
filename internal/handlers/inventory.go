package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ShowInventory renders the open inventory items & audit adjustments page
func ShowInventory(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	var items []models.InventoryItem
	var adjustments []models.InventoryAdjustment
	var restName string
	var lowStockCount int

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Fetch active restaurant name
		err := tx.QueryRowContext(c.Request.Context(), "SELECT name FROM restaurants WHERE id = $1", activeRestID).Scan(&restName)
		if err != nil {
			return err
		}

		// Fetch inventory items
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, name, unit, current_quantity, reorder_threshold, unit_cost, created_at
			FROM inventory_items
			WHERE restaurant_id = $1 AND deleted_at IS NULL
			ORDER BY name ASC
		`, activeRestID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var it models.InventoryItem
				it.RestaurantID = activeRestID
				var currentQty float64
				var threshold sql.NullFloat64
				var cost sql.NullInt64
				if err := rows.Scan(&it.ID, &it.Name, &it.Unit, &currentQty, &threshold, &cost, &it.CreatedAt); err != nil {
					fmt.Printf("rows.Scan error: %v\n", err)
				} else {
					it.CurrentQuantity = currentQty
					if threshold.Valid {
						it.ReorderThreshold = &threshold.Float64
						if it.CurrentQuantity <= threshold.Float64 {
							lowStockCount++
						}
					}
					if cost.Valid {
						cVal := int(cost.Int64)
						it.UnitCost = &cVal
					}
					items = append(items, it)
				}
			}
		}

		// Fetch recent adjustments audit log
		adjRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT a.id, a.inventory_item_id, i.name, i.unit, a.change_quantity, a.reason, a.note, COALESCE(u.full_name, 'Staff'), a.created_at
			FROM inventory_adjustments a
			JOIN inventory_items i ON i.id = a.inventory_item_id
			LEFT JOIN users u ON u.id = a.adjusted_by
			WHERE a.restaurant_id = $1
			ORDER BY a.created_at DESC
			LIMIT 30
		`, activeRestID)
		if err == nil {
			defer adjRows.Close()
			for adjRows.Next() {
				var adj models.InventoryAdjustment
				adj.RestaurantID = activeRestID
				var note sql.NullString
				var changeQty float64
				var userName string
				if err := adjRows.Scan(&adj.ID, &adj.InventoryItemID, &adj.ItemName, &adj.Unit, &changeQty, &adj.Reason, &note, &userName, &adj.CreatedAt); err != nil {
					fmt.Printf("adjRows.Scan error: %v\n", err)
				} else {
					adj.ChangeQuantity = changeQty
					if note.Valid {
						adj.Note = note.String
					}
					adj.AdjustedByName = userName
					adjustments = append(adjustments, adj)
				}
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load inventory: "+err.Error())
		return
	}

	c.HTML(http.StatusOK, "inventory.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"RestaurantName":     restName,
		"ActiveNav":          "inventory",
		"Items":              items,
		"Adjustments":        adjustments,
		"LowStockCount":      lowStockCount,
	})
}

// AddInventoryItem creates a new open inventory item
func AddInventoryItem(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	name := c.PostForm("name")
	unit := c.PostForm("unit")
	thresholdStr := c.PostForm("reorder_threshold")
	unitCostStr := c.PostForm("unit_cost")
	initialQtyStr := c.PostForm("initial_quantity")

	if name == "" || unit == "" {
		c.String(http.StatusBadRequest, "Item name and unit are required")
		return
	}

	var thresholdVal interface{}
	if thresholdStr != "" {
		if t, err := strconv.ParseFloat(thresholdStr, 64); err == nil {
			thresholdVal = t
		}
	}

	var unitCostVal interface{}
	if unitCostStr != "" {
		if costFloat, err := strconv.ParseFloat(unitCostStr, 64); err == nil {
			unitCostVal = int(costFloat * 100) // poisha
		}
	}

	initialQty := 0.0
	if initialQtyStr != "" {
		if q, err := strconv.ParseFloat(initialQtyStr, 64); err == nil {
			initialQty = q
		}
	}

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var itemID string
		err := tx.QueryRowContext(c.Request.Context(), `
			INSERT INTO inventory_items (restaurant_id, name, unit, current_quantity, reorder_threshold, unit_cost)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id
		`, activeRestID, name, unit, initialQty, thresholdVal, unitCostVal).Scan(&itemID)
		if err != nil {
			return err
		}

		if initialQty > 0 {
			_, err = tx.ExecContext(c.Request.Context(), `
				INSERT INTO inventory_adjustments (inventory_item_id, restaurant_id, change_quantity, reason, note, adjusted_by)
				VALUES ($1, $2, $3, 'purchase', 'Initial stock opening', $4)
			`, itemID, activeRestID, initialQty, user.ID)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to add inventory item: "+err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/inventory")
}

// AdjustInventoryStock records a movement (purchase, usage, wastage, correction) and updates quantity
func AdjustInventoryStock(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	itemID := c.PostForm("inventory_item_id")
	qtyStr := c.PostForm("change_quantity")
	reason := c.PostForm("reason")
	direction := c.PostForm("direction") // "in" or "out"
	note := c.PostForm("note")

	qty, err := strconv.ParseFloat(qtyStr, 64)
	if err != nil || qty <= 0 {
		c.String(http.StatusBadRequest, "Invalid quantity")
		return
	}

	if direction == "out" {
		qty = -qty
	}

	if reason != "purchase" && reason != "usage" && reason != "wastage" && reason != "correction" && reason != "other" {
		reason = "correction"
	}

	err = db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Update item running balance
		res, err := tx.ExecContext(c.Request.Context(), `
			UPDATE inventory_items
			SET current_quantity = current_quantity + $1
			WHERE id = $2 AND restaurant_id = $3 AND deleted_at IS NULL
		`, qty, itemID, activeRestID)
		if err != nil {
			return err
		}
		rowsAffected, _ := res.RowsAffected()
		if rowsAffected == 0 {
			return fmt.Errorf("inventory item not found")
		}

		// Log immutable audit adjustment trail
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO inventory_adjustments (inventory_item_id, restaurant_id, change_quantity, reason, note, adjusted_by)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, itemID, activeRestID, qty, reason, note, user.ID)
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to record stock adjustment: "+err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/inventory")
}

// DeleteInventoryItem soft-deletes an inventory item
func DeleteInventoryItem(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	itemID := c.Param("id")

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(c.Request.Context(), `
			UPDATE inventory_items
			SET deleted_at = now()
			WHERE id = $1 AND restaurant_id = $2
		`, itemID, activeRestID)
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to delete item: "+err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/inventory")
}
