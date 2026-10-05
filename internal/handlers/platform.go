package handlers

import (
	"crypto/rand"
	"database/sql"
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
	ID                 string
	Name               string
	Status             string
	OwnerName          string
	OwnerUsername      string
	OwnerEmail         string
	Restaurants        string
	RestaurantCount    int
	SubscriptionID     string
	PlanID             string
	PlanName           string
	SubscriptionStatus string
	PeriodStart        string
	PeriodEnd          string
}

type SubscriptionPlan struct {
	ID              string
	Code            string
	Name            string
	PriceAmount     int
	BillingInterval string
	IsActive        bool
	Features        []PlanFeatureValue
}

type PlatformFeature struct {
	ID          string
	Key         string
	Name        string
	Description string
	ValueType   string
}

type PlanFeatureValue struct {
	ID        string
	Key       string
	Name      string
	ValueType string
	Value     string
}

type AccountFeatureOverride struct {
	ID          string
	AccountName string
	FeatureName string
	Value       string
	Reason      string
}

type PlatformSupportUser struct {
	Name     string
	Username string
	Email    string
}

func platformUser(c *gin.Context) (CurrentUser, bool) {
	value, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/platform/login")
		return CurrentUser{}, false
	}
	user := value.(CurrentUser)
	if !user.IsPlatformAdmin {
		c.AbortWithStatus(http.StatusForbidden)
		return CurrentUser{}, false
	}
	return user, true
}

func superadmin(c *gin.Context) (CurrentUser, bool) {
	user, ok := platformUser(c)
	if !ok {
		return CurrentUser{}, false
	}
	if user.PlatformRole != "superadmin" {
		c.AbortWithStatus(http.StatusForbidden)
		return CurrentUser{}, false
	}
	return user, true
}

func ShowPlatformDashboard(c *gin.Context) {
	user, ok := platformUser(c)
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
	err = db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		for index := range accounts {
			var subscriptionID, planID, planName, status sql.NullString
			var start, end sql.NullTime
			err := tx.QueryRowContext(c.Request.Context(), `
				SELECT s.id, p.id, p.name, s.status, s.current_period_start, s.current_period_end
				FROM account_subscriptions s JOIN subscription_plans p ON p.id = s.plan_id
				WHERE s.account_id = $1 ORDER BY s.created_at DESC LIMIT 1
			`, accounts[index].ID).Scan(&subscriptionID, &planID, &planName, &status, &start, &end)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			if subscriptionID.Valid {
				accounts[index].SubscriptionID = subscriptionID.String
			}
			if planID.Valid {
				accounts[index].PlanID = planID.String
			}
			if planName.Valid {
				accounts[index].PlanName = planName.String
			}
			if status.Valid {
				accounts[index].SubscriptionStatus = status.String
			}
			if start.Valid {
				accounts[index].PeriodStart = start.Time.Format("2006-01-02")
			}
			if end.Valid {
				accounts[index].PeriodEnd = end.Time.Format("2006-01-02")
			}
		}
		return nil
	})
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load account subscriptions")
		return
	}

	plans := make([]SubscriptionPlan, 0)
	planRows, err := db.DB.QueryContext(c.Request.Context(), "SELECT id, code, name, price_amount, billing_interval, is_active FROM subscription_plans ORDER BY sort_order, name")
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load subscription plans")
		return
	}
	defer planRows.Close()
	for planRows.Next() {
		var plan SubscriptionPlan
		if err := planRows.Scan(&plan.ID, &plan.Code, &plan.Name, &plan.PriceAmount, &plan.BillingInterval, &plan.IsActive); err != nil {
			c.String(http.StatusInternalServerError, "Failed to read subscription plans")
			return
		}
		plans = append(plans, plan)
	}
	if err := planRows.Err(); err != nil {
		c.String(http.StatusInternalServerError, "Failed to read subscription plans")
		return
	}
	planRows.Close()

	features := make([]PlatformFeature, 0)
	featureRows, err := db.DB.QueryContext(c.Request.Context(), "SELECT id, key, name, COALESCE(description, ''), value_type FROM features ORDER BY name")
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load features")
		return
	}
	for featureRows.Next() {
		var feature PlatformFeature
		if err := featureRows.Scan(&feature.ID, &feature.Key, &feature.Name, &feature.Description, &feature.ValueType); err != nil {
			featureRows.Close()
			c.String(http.StatusInternalServerError, "Failed to read features")
			return
		}
		features = append(features, feature)
	}
	if err := featureRows.Err(); err != nil {
		featureRows.Close()
		c.String(http.StatusInternalServerError, "Failed to read features")
		return
	}
	featureRows.Close()
	for planIndex := range plans {
		for _, feature := range features {
			var value string
			err := db.DB.QueryRowContext(c.Request.Context(), `
				SELECT value FROM plan_features WHERE plan_id = $1 AND feature_id = $2
			`, plans[planIndex].ID, feature.ID).Scan(&value)
			if err == sql.ErrNoRows {
				value = ""
			} else if err != nil {
				c.String(http.StatusInternalServerError, "Failed to load plan features")
				return
			}
			plans[planIndex].Features = append(plans[planIndex].Features, PlanFeatureValue{ID: feature.ID, Key: feature.Key, Name: feature.Name, ValueType: feature.ValueType, Value: value})
		}
	}

	overrides := make([]AccountFeatureOverride, 0)
	err = db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT o.id, a.name, f.name, o.value, o.reason
			FROM account_feature_overrides o JOIN accounts a ON a.id = o.account_id
			JOIN features f ON f.id = o.feature_id ORDER BY a.name, f.name
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var override AccountFeatureOverride
			if err := rows.Scan(&override.ID, &override.AccountName, &override.FeatureName, &override.Value, &override.Reason); err != nil {
				return err
			}
			overrides = append(overrides, override)
		}
		return rows.Err()
	})
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load account feature overrides")
		return
	}

	supportUsers := make([]PlatformSupportUser, 0)
	supportRows, err := db.DB.QueryContext(c.Request.Context(), `
		SELECT u.full_name, u.username, u.email FROM platform_admins pa
		JOIN users u ON u.id = pa.user_id WHERE pa.role = 'support' ORDER BY u.full_name
	`)
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load support users")
		return
	}
	for supportRows.Next() {
		var support PlatformSupportUser
		if err := supportRows.Scan(&support.Name, &support.Username, &support.Email); err != nil {
			supportRows.Close()
			c.String(http.StatusInternalServerError, "Failed to read support users")
			return
		}
		supportUsers = append(supportUsers, support)
	}
	if err := supportRows.Err(); err != nil {
		supportRows.Close()
		c.String(http.StatusInternalServerError, "Failed to read support users")
		return
	}
	supportRows.Close()

	c.HTML(http.StatusOK, "platform_dashboard.tmpl", gin.H{
		"User": user, "Accounts": accounts, "Plans": plans, "Features": features,
		"Overrides": overrides, "SupportUsers": supportUsers, "CanManagePlatform": user.PlatformRole == "superadmin",
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
