package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Helper to get active restaurant ID
func GetActiveRestaurantID(c *gin.Context, user CurrentUser) string {
	if len(user.Restaurants) == 0 {
		return ""
	}
	// Check cookie
	cookieID, err := c.Cookie("rms_active_restaurant")
	if err == nil && cookieID != "" {
		// Verify user belongs to this restaurant
		for _, r := range user.Restaurants {
			if r.RestaurantID == cookieID {
				return r.RestaurantID
			}
		}
	}
	// Fallback to first
	return user.Restaurants[0].RestaurantID
}

// GetLimit returns the integer limit or boolean state (1/0) for a feature on an account
func GetLimit(ctx context.Context, accountID string, featureKey string) (int, error) {
	var overrideVal string
	err := db.DB.QueryRowContext(ctx, `
		SELECT o.value FROM account_feature_overrides o
		JOIN features f ON f.id = o.feature_id
		WHERE o.account_id = $1 AND f.key = $2
	`, accountID, featureKey).Scan(&overrideVal)
	if err == nil {
		if overrideVal == "true" {
			return 1, nil
		}
		if overrideVal == "false" {
			return 0, nil
		}
		val, err := strconv.Atoi(overrideVal)
		if err == nil {
			return val, nil
		}
	}

	var planVal string
	err = db.DB.QueryRowContext(ctx, `
		SELECT pf.value FROM plan_features pf
		JOIN features f ON f.id = pf.feature_id
		JOIN account_subscriptions s ON s.plan_id = pf.plan_id
		WHERE s.account_id = $1 AND s.status IN ('trialing','active','past_due') AND f.key = $2
	`, accountID, featureKey).Scan(&planVal)
	if err == nil {
		if planVal == "true" {
			return 1, nil
		}
		if planVal == "false" {
			return 0, nil
		}
		val, err := strconv.Atoi(planVal)
		if err == nil {
			return val, nil
		}
	}

	// Fallback/Default
	return 0, fmt.Errorf("feature %s limit not found", featureKey)
}

// ShowTables renders the tables grid page
func ShowTables(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	var tables []models.Table
	var restName string
	var limitErr string

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Fetch active restaurant name
		err := tx.QueryRowContext(c.Request.Context(), "SELECT name FROM restaurants WHERE id = $1", activeRestID).Scan(&restName)
		if err != nil {
			return err
		}

		// Fetch tables
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, name, capacity, status FROM tables 
			WHERE restaurant_id = $1 ORDER BY name
		`, activeRestID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t models.Table
			t.RestaurantID = activeRestID
			if err := rows.Scan(&t.ID, &t.Name, &t.Capacity, &t.Status); err == nil {
				tables = append(tables, t)
			}
		}
		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load tables: "+err.Error())
		return
	}

	c.HTML(http.StatusOK, "tables.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"RestaurantName":     restName,
		"ActiveNav":          "tables",
		"Tables":             tables,
		"LimitError":         limitErr,
	})
}

// AddTable adds a table after verifying subscription limits
func AddTable(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	tableName := c.PostForm("name")
	capacityStr := c.PostForm("capacity")
	capacity, _ := strconv.Atoi(capacityStr)

	if tableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name is required"})
		return
	}

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// 1. Fetch account ID
		var accountID string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT account_id FROM restaurants WHERE id = $1", activeRestID).Scan(&accountID)
		if err != nil {
			return fmt.Errorf("failed to fetch account: %w", err)
		}

		// 2. Check table count limits
		var count int
		err = tx.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM tables WHERE restaurant_id = $1", activeRestID).Scan(&count)
		if err != nil {
			return err
		}

		limit, err := GetLimit(c.Request.Context(), accountID, "max_tables_per_restaurant")
		if err != nil {
			return err
		}

		if count >= limit {
			return fmt.Errorf("table limit of %d reached. Upgrade your subscription plan under Billing to add more tables", limit)
		}

		// 3. Insert table
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO tables (restaurant_id, name, capacity, status) 
			VALUES ($1, $2, $3, 'available')
		`, activeRestID, tableName, capacity)
		return err
	})

	if err != nil {
		c.HTML(http.StatusOK, "tables.tmpl", gin.H{
			"AlertError": err.Error(),
		})
		// We want HTMX to redirect or refresh the page, or we redirect back.
		c.Redirect(http.StatusSeeOther, "/tables")
		return
	}

	c.Redirect(http.StatusSeeOther, "/tables")
}

// UpdateTableStatus updates status of a table via HTMX
func UpdateTableStatus(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	tableID := c.Param("id")
	newStatus := c.Query("status")

	if newStatus != "available" && newStatus != "occupied" && newStatus != "reserved" {
		c.String(http.StatusBadRequest, "Invalid status")
		return
	}

	var table models.Table
	table.ID = tableID
	table.Status = newStatus

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Update table status
		_, err := tx.ExecContext(c.Request.Context(), `
			UPDATE tables SET status = $1 WHERE id = $2 AND restaurant_id = $3
		`, newStatus, tableID, activeRestID)
		if err != nil {
			return err
		}

		// Fetch updated details
		return tx.QueryRowContext(c.Request.Context(), `
			SELECT name, capacity FROM tables WHERE id = $1
		`, tableID).Scan(&table.Name, &table.Capacity)
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// Render only the table card component back to the HTMX client
	c.HTML(http.StatusOK, "table-card", table)
}

// SwitchRestaurant handles updating active restaurant cookie
func SwitchRestaurant(c *gin.Context) {
	restID := c.PostForm("restaurant_id")
	if restID != "" {
		c.SetCookie("rms_active_restaurant", restID, 86400*30, "/", "", false, true)
	}
	c.Redirect(http.StatusSeeOther, c.Request.Referer())
}
