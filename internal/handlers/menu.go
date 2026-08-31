package handlers

import (
	"database/sql"
	"net/http"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// ShowMenu renders the menu management screen
func ShowMenu(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	var categories []models.MenuCategory
	var items []models.MenuItem

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Load categories
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, name, sort_order FROM menu_categories
			WHERE restaurant_id = $1 AND deleted_at IS NULL
			ORDER BY sort_order, name
		`, activeRestID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cat models.MenuCategory
			cat.RestaurantID = activeRestID
			if err := rows.Scan(&cat.ID, &cat.Name, &cat.SortOrder); err == nil {
				categories = append(categories, cat)
			}
		}

		// Load menu items
		rowsItems, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, category_id, name, description, price, is_available FROM menu_items
			WHERE restaurant_id = $1 AND deleted_at IS NULL
			ORDER BY name
		`, activeRestID)
		if err != nil {
			return err
		}
		defer rowsItems.Close()
		for rowsItems.Next() {
			var item models.MenuItem
			item.RestaurantID = activeRestID
			var desc sql.NullString
			var catID sql.NullString
			if err := rowsItems.Scan(&item.ID, &catID, &item.Name, &desc, &item.Price, &item.IsAvailable); err == nil {
				if desc.Valid {
					item.Description = &desc.String
				}
				if catID.Valid {
					item.CategoryID = &catID.String
				}
				items = append(items, item)
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load menu: "+err.Error())
		return
	}

	c.HTML(http.StatusOK, "menu.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"ActiveNav":          "menu",
		"Categories":         categories,
		"MenuItems":          items,
	})
}

// AddCategory adds a menu category
func AddCategory(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	name := c.PostForm("name")
	sortOrder, _ := strconv.Atoi(c.PostForm("sort_order"))

	if name == "" {
		c.Redirect(http.StatusSeeOther, "/menu")
		return
	}

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO menu_categories (restaurant_id, name, sort_order)
			VALUES ($1, $2, $3)
		`, activeRestID, name, sortOrder)
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/menu")
}

// DeleteCategory soft deletes a menu category
func DeleteCategory(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	id := c.Param("id")

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(c.Request.Context(), `
			UPDATE menu_categories SET deleted_at = $1 
			WHERE id = $2 AND restaurant_id = $3
		`, time.Now(), id, activeRestID)
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/menu")
}

// AddMenuItem adds a menu item (converting float/decimal price to poisha integer)
func AddMenuItem(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	name := c.PostForm("name")
	desc := c.PostForm("description")
	priceFloat, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	pricePoisha := int(priceFloat * 100) // Convert decimal price to integer poisha
	categoryID := c.PostForm("category_id")

	if name == "" {
		c.Redirect(http.StatusSeeOther, "/menu")
		return
	}

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var catIDVal interface{}
		if categoryID != "" {
			catIDVal = categoryID
		} else {
			catIDVal = nil
		}

		_, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO menu_items (restaurant_id, category_id, name, description, price, is_available)
			VALUES ($1, $2, $3, $4, $5, true)
		`, activeRestID, catIDVal, name, desc, pricePoisha)
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/menu")
}

// DeleteMenuItem soft deletes a menu item
func DeleteMenuItem(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	id := c.Param("id")

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(c.Request.Context(), `
			UPDATE menu_items SET deleted_at = $1 
			WHERE id = $2 AND restaurant_id = $3
		`, time.Now(), id, activeRestID)
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/menu")
}
