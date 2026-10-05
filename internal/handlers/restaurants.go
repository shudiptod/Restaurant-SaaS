package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"

	"github.com/gin-gonic/gin"
)

func restaurantAccount(ctx context.Context, tx *sql.Tx, restaurantID, userID string) (string, error) {
	var accountID, ownerID string
	err := tx.QueryRowContext(ctx, `
		SELECT r.account_id, a.owner_user_id FROM restaurants r
		JOIN accounts a ON a.id = r.account_id WHERE r.id = $1
	`, restaurantID).Scan(&accountID, &ownerID)
	if err != nil {
		return "", err
	}
	if ownerID != userID {
		return "", fmt.Errorf("only the account owner can manage restaurants")
	}
	return accountID, nil
}

func ShowNewRestaurant(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := value.(CurrentUser)
	activeRestaurantID := GetActiveRestaurantID(c, user)
	var restaurants []models.RestaurantUserContext
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		accountID, err := restaurantAccount(c.Request.Context(), tx, activeRestaurantID, user.ID)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(c.Request.Context(), "SELECT id, name FROM restaurants WHERE account_id = $1 ORDER BY name", accountID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var restaurant models.RestaurantUserContext
			if err := rows.Scan(&restaurant.RestaurantID, &restaurant.RestaurantName); err != nil {
				return err
			}
			restaurants = append(restaurants, restaurant)
		}
		return rows.Err()
	})
	if err != nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.HTML(http.StatusOK, "restaurant_new.tmpl", gin.H{
		"User": user, "ActiveRestaurantID": activeRestaurantID, "ActiveNav": "",
		"Restaurants": restaurants, "Error": c.Query("error"),
	})
}

func CreateRestaurant(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := value.(CurrentUser)
	activeRestaurantID := GetActiveRestaurantID(c, user)
	name := strings.TrimSpace(c.PostForm("name"))
	sourceID := strings.TrimSpace(c.PostForm("copy_from"))
	if name == "" {
		c.Redirect(http.StatusSeeOther, "/restaurants/new?error=Restaurant+name+is+required")
		return
	}
	slug, err := restaurantSlug(name)
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not create restaurant identifier")
		return
	}
	var createdRestaurantID string
	err = db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		accountID, err := restaurantAccount(c.Request.Context(), tx, activeRestaurantID, user.ID)
		if err != nil {
			return err
		}
		var lockedAccount string
		if err := tx.QueryRowContext(c.Request.Context(), "SELECT id FROM accounts WHERE id = $1 FOR UPDATE", accountID).Scan(&lockedAccount); err != nil {
			return err
		}
		limit, err := GetLimit(c.Request.Context(), accountID, "max_restaurants")
		if err != nil {
			return fmt.Errorf("restaurant limit is unavailable for this account")
		}
		var count int
		if err := tx.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM restaurants WHERE account_id = $1", accountID).Scan(&count); err != nil {
			return err
		}
		if count >= limit {
			return fmt.Errorf("restaurant limit of %d reached; upgrade the subscription to add another restaurant", limit)
		}
		if sourceID != "" {
			var sourceAccount string
			if err := tx.QueryRowContext(c.Request.Context(), "SELECT account_id FROM restaurants WHERE id = $1", sourceID).Scan(&sourceAccount); err != nil {
				return fmt.Errorf("copy source restaurant not found")
			}
			if sourceAccount != accountID {
				return fmt.Errorf("copy source must belong to this account")
			}
		}
		if err := tx.QueryRowContext(c.Request.Context(), `
			INSERT INTO restaurants (account_id, name, slug) VALUES ($1, $2, $3) RETURNING id
		`, accountID, name, slug).Scan(&createdRestaurantID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Request.Context(), "INSERT INTO restaurant_users (restaurant_id, user_id, role) VALUES ($1, $2, 'owner')", createdRestaurantID, user.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Request.Context(), "INSERT INTO restaurant_tax_settings (restaurant_id) VALUES ($1)", createdRestaurantID); err != nil {
			return err
		}
		if sourceID == "" {
			return nil
		}

		if _, err := tx.ExecContext(c.Request.Context(), `
			UPDATE restaurant_tax_settings target
			SET vat_rate_bps = source.vat_rate_bps, vat_inclusive = source.vat_inclusive,
			    service_charge_rate_bps = source.service_charge_rate_bps, updated_at = now()
			FROM restaurant_tax_settings source WHERE target.restaurant_id = $1 AND source.restaurant_id = $2
		`, createdRestaurantID, sourceID); err != nil {
			return err
		}

		type sourceCategory struct {
			id, name  string
			sortOrder int
		}
		categories := make([]sourceCategory, 0)
		categoryRows, err := tx.QueryContext(c.Request.Context(), "SELECT id, name, sort_order FROM menu_categories WHERE restaurant_id = $1 AND deleted_at IS NULL", sourceID)
		if err != nil {
			return err
		}
		for categoryRows.Next() {
			var category sourceCategory
			if err := categoryRows.Scan(&category.id, &category.name, &category.sortOrder); err != nil {
				categoryRows.Close()
				return err
			}
			categories = append(categories, category)
		}
		if err := categoryRows.Err(); err != nil {
			categoryRows.Close()
			return err
		}
		categoryRows.Close()
		categoryIDs := make(map[string]string, len(categories))
		for _, category := range categories {
			var newID string
			if err := tx.QueryRowContext(c.Request.Context(), "INSERT INTO menu_categories (restaurant_id, name, sort_order) VALUES ($1, $2, $3) RETURNING id", createdRestaurantID, category.name, category.sortOrder).Scan(&newID); err != nil {
				return err
			}
			categoryIDs[category.id] = newID
		}

		itemRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT category_id, name, description, price, is_available FROM menu_items
			WHERE restaurant_id = $1 AND deleted_at IS NULL
		`, sourceID)
		if err != nil {
			return err
		}
		type sourceItem struct {
			category    sql.NullString
			name        string
			description sql.NullString
			price       int
			available   bool
		}
		items := make([]sourceItem, 0)
		for itemRows.Next() {
			var item sourceItem
			if err := itemRows.Scan(&item.category, &item.name, &item.description, &item.price, &item.available); err != nil {
				itemRows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := itemRows.Err(); err != nil {
			itemRows.Close()
			return err
		}
		itemRows.Close()
		for _, item := range items {
			var categoryValue, descriptionValue interface{}
			if item.category.Valid {
				categoryValue = categoryIDs[item.category.String]
			}
			if item.description.Valid {
				descriptionValue = item.description.String
			}
			if _, err := tx.ExecContext(c.Request.Context(), `
				INSERT INTO menu_items (restaurant_id, category_id, name, description, price, is_available)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, createdRestaurantID, categoryValue, item.name, descriptionValue, item.price, item.available); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO tables (restaurant_id, name, capacity, status)
			SELECT $1, name, capacity, 'available' FROM tables WHERE restaurant_id = $2
		`, createdRestaurantID, sourceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO inventory_items (restaurant_id, name, unit, current_quantity, reorder_threshold, unit_cost)
			SELECT $1, name, unit, current_quantity, reorder_threshold, unit_cost
			FROM inventory_items WHERE restaurant_id = $2 AND deleted_at IS NULL
		`, createdRestaurantID, sourceID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/restaurants/new?error="+url.QueryEscape(err.Error()))
		return
	}
	c.SetCookie("rms_active_restaurant", createdRestaurantID, 86400*30, "/", "", false, true)
	c.Redirect(http.StatusSeeOther, "/")
}
