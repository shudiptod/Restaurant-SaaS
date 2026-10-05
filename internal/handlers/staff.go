package handlers

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"

	"github.com/gin-gonic/gin"
)

func canManageRestaurant(user CurrentUser, restaurantID string) bool {
	for _, restaurant := range user.Restaurants {
		if restaurant.RestaurantID == restaurantID && (restaurant.Role == "owner" || restaurant.Role == "admin") {
			return true
		}
	}
	return false
}

func ShowStaff(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := value.(CurrentUser)
	restaurantID := GetActiveRestaurantID(c, user)
	if !canManageRestaurant(user, restaurantID) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	var staff []models.RestaurantStaff
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, restaurant_id, name, is_waiter, is_cashier, custom_title, is_active
			FROM restaurant_staff WHERE restaurant_id = $1 ORDER BY is_active DESC, name
		`, restaurantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var member models.RestaurantStaff
			var title sql.NullString
			if err := rows.Scan(&member.ID, &member.RestaurantID, &member.Name, &member.IsWaiter, &member.IsCashier, &title, &member.IsActive); err != nil {
				return err
			}
			if title.Valid {
				member.CustomTitle = &title.String
			}
			staff = append(staff, member)
		}
		return rows.Err()
	})
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load staff: "+err.Error())
		return
	}
	c.HTML(http.StatusOK, "staff.tmpl", gin.H{
		"User": user, "ActiveRestaurantID": restaurantID, "ActiveNav": "staff",
		"Staff": staff, "Error": c.Query("error"), "Notice": c.Query("notice"),
	})
}

func AddRestaurantStaff(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := value.(CurrentUser)
	restaurantID := GetActiveRestaurantID(c, user)
	if !canManageRestaurant(user, restaurantID) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	title := strings.TrimSpace(c.PostForm("custom_title"))
	isWaiter := c.PostForm("is_waiter") == "true"
	isCashier := c.PostForm("is_cashier") == "true"
	if name == "" || (!isWaiter && !isCashier && title == "") {
		c.Redirect(http.StatusSeeOther, "/staff?error="+url.QueryEscape("Enter a name and at least one role or custom title"))
		return
	}
	var titleValue interface{}
	if title != "" {
		titleValue = title
	}
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO restaurant_staff (restaurant_id, name, is_waiter, is_cashier, custom_title)
			VALUES ($1, $2, $3, $4, $5)
		`, restaurantID, name, isWaiter, isCashier, titleValue)
		return err
	})
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/staff?error="+url.QueryEscape("Could not add staff member"))
		return
	}
	c.Redirect(http.StatusSeeOther, "/staff?notice="+url.QueryEscape("Staff member added"))
}

func UpdateRestaurantStaffStatus(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := value.(CurrentUser)
	restaurantID := GetActiveRestaurantID(c, user)
	if !canManageRestaurant(user, restaurantID) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	status := c.PostForm("status")
	if status != "active" && status != "inactive" {
		c.Redirect(http.StatusSeeOther, "/staff?error=Invalid+staff+status")
		return
	}
	var found bool
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(c.Request.Context(), `
			UPDATE restaurant_staff SET is_active = $1 WHERE id = $2 AND restaurant_id = $3
		`, status == "active", c.Param("id"), restaurantID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		found = count > 0
		return err
	})
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not update staff status")
		return
	}
	if !found {
		c.Redirect(http.StatusSeeOther, "/staff?error=Staff+member+not+found")
		return
	}
	c.Redirect(http.StatusSeeOther, "/staff?notice=Staff+status+updated")
}
