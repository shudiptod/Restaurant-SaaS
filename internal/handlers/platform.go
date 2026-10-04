package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"restaurant-saas/internal/db"
)

type PlatformAccount struct {
	ID              string
	Name            string
	Status          string
	OwnerName       string
	OwnerUsername   string
	OwnerEmail      string
	Restaurants     string
	RestaurantCount int
}

type SubscriptionPlan struct {
	Code string
	Name string
}

func superadmin(c *gin.Context) (CurrentUser, bool) {
	value, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return CurrentUser{}, false
	}
	user := value.(CurrentUser)
	if !user.IsPlatformAdmin || user.PlatformRole != "superadmin" {
		c.AbortWithStatus(http.StatusForbidden)
		return CurrentUser{}, false
	}
	return user, true
}

func ShowPlatformDashboard(c *gin.Context) {
	user, ok := superadmin(c)
	if !ok {
		return
	}

	accounts := make([]PlatformAccount, 0)
	rows, err := db.DB.QueryContext(c.Request.Context(), `
		SELECT a.id, a.name, a.status, u.full_name, u.username, u.email,
		       COALESCE(string_agg(DISTINCT r.name, ', ' ORDER BY r.name), ''),
		       count(DISTINCT r.id)
		FROM accounts a
	JOIN users u ON u.id = a.owner_user_id
	LEFT JOIN restaurants r ON r.account_id = a.id
	GROUP BY a.id, u.id
	ORDER BY a.created_at DESC
	`)
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load customer accounts")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var account PlatformAccount
		if err := rows.Scan(&account.ID, &account.Name, &account.Status, &account.OwnerName, &account.OwnerUsername, &account.OwnerEmail, &account.Restaurants, &account.RestaurantCount); err != nil {
			c.String(http.StatusInternalServerError, "Failed to read customer accounts")
			return
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		c.String(http.StatusInternalServerError, "Failed to read customer accounts")
		return
	}

	plans := make([]SubscriptionPlan, 0)
	planRows, err := db.DB.QueryContext(c.Request.Context(), "SELECT code, name FROM subscription_plans WHERE is_active = true ORDER BY sort_order, name")
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load subscription plans")
		return
	}
	defer planRows.Close()
	for planRows.Next() {
		var plan SubscriptionPlan
		if err := planRows.Scan(&plan.Code, &plan.Name); err != nil {
			c.String(http.StatusInternalServerError, "Failed to read subscription plans")
			return
		}
		plans = append(plans, plan)
	}

	c.HTML(http.StatusOK, "platform_dashboard.tmpl", gin.H{
		"User": user, "Accounts": accounts, "Plans": plans,
		"Error": c.Query("error"), "Notice": c.Query("notice"), "ActiveNav": "platform",
	})
}

func CreateCustomerAccount(c *gin.Context) {
	user, ok := superadmin(c)
	if !ok {
		return
	}

	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.TrimSpace(c.PostForm("email"))
	ownerName := strings.TrimSpace(c.PostForm("owner_name"))
	accountName := strings.TrimSpace(c.PostForm("account_name"))
	restaurantName := strings.TrimSpace(c.PostForm("restaurant_name"))
	password := c.PostForm("password")
	planCode := strings.TrimSpace(c.PostForm("plan_code"))
	if username == "" || email == "" || ownerName == "" || accountName == "" || restaurantName == "" || planCode == "" || len(password) < 12 {
		c.Redirect(http.StatusSeeOther, "/platform?error=Complete+all+fields+and+use+a+password+of+at+least+12+characters")
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/platform?error=Could+not+secure+the+password")
		return
	}
	slug, err := restaurantSlug(restaurantName)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/platform?error=Could+not+create+restaurant+identifier")
		return
	}

	tx, err := db.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not start account creation")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(c.Request.Context(), "SELECT set_config('app.current_user_id', $1, true)", user.ID); err != nil {
		c.String(http.StatusInternalServerError, "Could not establish platform permissions")
		return
	}
	var ownerID, accountID, planID string
	err = tx.QueryRowContext(c.Request.Context(), `
		INSERT INTO users (username, email, password_hash, full_name)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, username, email, string(passwordHash), ownerName).Scan(&ownerID)
	if err == nil {
		err = tx.QueryRowContext(c.Request.Context(), `
			INSERT INTO accounts (name, owner_user_id) VALUES ($1, $2) RETURNING id
		`, accountName, ownerID).Scan(&accountID)
	}
	if err == nil {
		err = tx.QueryRowContext(c.Request.Context(), "SELECT id FROM subscription_plans WHERE code = $1 AND is_active = true", planCode).Scan(&planID)
	}
	var restaurantID string
	if err == nil {
		err = tx.QueryRowContext(c.Request.Context(), `
			INSERT INTO restaurants (account_id, name, slug) VALUES ($1, $2, $3) RETURNING id
		`, accountID, restaurantName, slug).Scan(&restaurantID)
	}
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO restaurant_users (restaurant_id, user_id, role) VALUES ($1, $2, 'owner')
		`, restaurantID, ownerID)
	}
	now := time.Now()
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO account_subscriptions (account_id, plan_id, status, current_period_start, current_period_end)
			VALUES ($1, $2, 'trialing', $3, $4)
		`, accountID, planID, now, now.AddDate(0, 0, 30))
	}
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO restaurant_tax_settings (restaurant_id) VALUES ($1)
		`, restaurantID)
	}
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/platform?error=Account+creation+failed%3A+check+for+duplicate+username+or+email")
		return
	}
	if err := tx.Commit(); err != nil {
		c.Redirect(http.StatusSeeOther, "/platform?error=Could+not+save+customer+account")
		return
	}
	c.Redirect(http.StatusSeeOther, "/platform?notice=Customer+account+created")
}

func UpdateAccountStatus(c *gin.Context) {
	user, ok := superadmin(c)
	if !ok {
		return
	}
	accountID := c.Param("id")
	newStatus := c.PostForm("status")
	if newStatus != "active" && newStatus != "suspended" {
		c.Redirect(http.StatusSeeOther, "/platform?error=Invalid+account+status")
		return
	}

	tx, err := db.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not update account status")
		return
	}
	defer tx.Rollback()
	var oldStatus string
	err = tx.QueryRowContext(c.Request.Context(), "SELECT status FROM accounts WHERE id = $1 FOR UPDATE", accountID).Scan(&oldStatus)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/platform?error=Account+not+found")
		return
	}
	_, err = tx.ExecContext(c.Request.Context(), `
		UPDATE accounts SET status = $1, locked_reason = CASE WHEN $1 = 'active' THEN NULL ELSE 'Disabled by platform owner' END,
		updated_at = now() WHERE id = $2
	`, newStatus, accountID)
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not update account status")
		return
	}
	_, err = tx.ExecContext(c.Request.Context(), `
		INSERT INTO account_status_log (account_id, changed_by, old_status, new_status, reason)
		VALUES ($1, $2, $3, $4, $5)
	`, accountID, user.ID, oldStatus, newStatus, "Changed by platform owner")
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not record account status change")
		return
	}
	if err := tx.Commit(); err != nil {
		c.String(http.StatusInternalServerError, "Could not save account status")
		return
	}
	c.Redirect(http.StatusSeeOther, "/platform?notice=Account+status+updated")
}

var nonSlugCharacters = regexp.MustCompile(`[^a-z0-9]+`)

func restaurantSlug(name string) (string, error) {
	slug := strings.Trim(nonSlugCharacters.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "restaurant"
	}
	var randomSuffix [4]byte
	if _, err := rand.Read(randomSuffix[:]); err != nil {
		return "", fmt.Errorf("generate slug suffix: %w", err)
	}
	return slug + "-" + hex.EncodeToString(randomSuffix[:]), nil
}
