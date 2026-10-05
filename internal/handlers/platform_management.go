package handlers

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"restaurant-saas/internal/db"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

var featureKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func platformResult(c *gin.Context, err error, success string) {
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/platform?error="+url.QueryEscape(err.Error()))
		return
	}
	c.Redirect(http.StatusSeeOther, "/platform?notice="+url.QueryEscape(success))
}

func CreatePlatformSupport(c *gin.Context) {
	admin, ok := superadmin(c)
	if !ok {
		return
	}
	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.TrimSpace(c.PostForm("email"))
	name := strings.TrimSpace(c.PostForm("full_name"))
	password := c.PostForm("password")
	if username == "" || email == "" || name == "" || len(password) < 12 {
		platformResult(c, fmt.Errorf("complete all fields and use a password of at least 12 characters"), "")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		platformResult(c, fmt.Errorf("could not secure password"), "")
		return
	}
	tx, err := db.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		platformResult(c, fmt.Errorf("could not start support-user creation"), "")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(c.Request.Context(), "SELECT set_config('app.current_user_id', $1, true)", admin.ID); err != nil {
		platformResult(c, fmt.Errorf("could not establish platform permissions"), "")
		return
	}
	var userID string
	err = tx.QueryRowContext(c.Request.Context(), `
		INSERT INTO users (username, email, password_hash, full_name) VALUES ($1, $2, $3, $4) RETURNING id
	`, username, email, string(hash), name).Scan(&userID)
	if err == nil {
		_, err = tx.ExecContext(c.Request.Context(), "INSERT INTO platform_admins (user_id, role) VALUES ($1, 'support')", userID)
	}
	if err != nil {
		platformResult(c, fmt.Errorf("could not create support account; username or email may already be in use"), "")
		return
	}
	if err := tx.Commit(); err != nil {
		platformResult(c, fmt.Errorf("could not save support account"), "")
		return
	}
	platformResult(c, nil, "Support account created")
}

func CreateSubscriptionPlan(c *gin.Context) {
	if _, ok := superadmin(c); !ok {
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	code := strings.TrimSpace(c.PostForm("code"))
	interval := c.PostForm("billing_interval")
	price, err := parseBDTAmount(c.PostForm("price"))
	if name == "" || !featureKeyPattern.MatchString(code) || (interval != "monthly" && interval != "yearly") || err != nil {
		platformResult(c, fmt.Errorf("enter a valid plan name, code, price, and billing interval"), "")
		return
	}
	_, err = db.DB.ExecContext(c.Request.Context(), `
		INSERT INTO subscription_plans (name, code, price_amount, billing_interval)
		VALUES ($1, $2, $3, $4)
	`, name, code, price, interval)
	if err != nil {
		platformResult(c, fmt.Errorf("could not create plan; code may already exist"), "")
		return
	}
	platformResult(c, nil, "Subscription plan created")
}

func UpdateSubscriptionPlan(c *gin.Context) {
	if _, ok := superadmin(c); !ok {
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	interval := c.PostForm("billing_interval")
	price, err := parseBDTAmount(c.PostForm("price"))
	isActive := c.PostForm("is_active") == "true"
	if name == "" || (interval != "monthly" && interval != "yearly") || err != nil {
		platformResult(c, fmt.Errorf("enter a valid plan name, price, and billing interval"), "")
		return
	}
	result, err := db.DB.ExecContext(c.Request.Context(), `
		UPDATE subscription_plans SET name = $1, price_amount = $2, billing_interval = $3,
		is_active = $4, updated_at = now() WHERE id = $5
	`, name, price, interval, isActive, c.Param("id"))
	if err != nil {
		platformResult(c, fmt.Errorf("could not update plan"), "")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		platformResult(c, fmt.Errorf("plan not found"), "")
		return
	}
	platformResult(c, nil, "Plan updated")
}

func CreatePlatformFeature(c *gin.Context) {
	if _, ok := superadmin(c); !ok {
		return
	}
	key := strings.TrimSpace(c.PostForm("key"))
	name := strings.TrimSpace(c.PostForm("name"))
	description := strings.TrimSpace(c.PostForm("description"))
	valueType := c.PostForm("value_type")
	if !featureKeyPattern.MatchString(key) || name == "" || !validFeatureType(valueType) {
		platformResult(c, fmt.Errorf("enter a valid feature key, name, and value type"), "")
		return
	}
	var descriptionValue interface{}
	if description != "" {
		descriptionValue = description
	}
	_, err := db.DB.ExecContext(c.Request.Context(), `
		INSERT INTO features (key, name, description, value_type) VALUES ($1, $2, $3, $4)
	`, key, name, descriptionValue, valueType)
	if err != nil {
		platformResult(c, fmt.Errorf("could not create feature; key may already exist"), "")
		return
	}
	platformResult(c, nil, "Feature added")
}

func UpdatePlatformFeature(c *gin.Context) {
	if _, ok := superadmin(c); !ok {
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	description := strings.TrimSpace(c.PostForm("description"))
	valueType := c.PostForm("value_type")
	if name == "" || !validFeatureType(valueType) {
		platformResult(c, fmt.Errorf("enter a feature name and valid value type"), "")
		return
	}
	var descriptionValue interface{}
	if description != "" {
		descriptionValue = description
	}
	result, err := db.DB.ExecContext(c.Request.Context(), `
		UPDATE features SET name = $1, description = $2, value_type = $3 WHERE id = $4
	`, name, descriptionValue, valueType, c.Param("id"))
	if err != nil {
		platformResult(c, fmt.Errorf("could not update feature"), "")
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		platformResult(c, fmt.Errorf("feature not found"), "")
		return
	}
	platformResult(c, nil, "Feature updated")
}

func UpdatePlanFeatures(c *gin.Context) {
	if _, ok := superadmin(c); !ok {
		return
	}
	tx, err := db.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		platformResult(c, fmt.Errorf("could not start plan update"), "")
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(c.Request.Context(), "SELECT id, key, value_type FROM features ORDER BY key")
	if err != nil {
		platformResult(c, fmt.Errorf("could not load feature catalog"), "")
		return
	}
	type featureInput struct{ id, key, valueType, value string }
	inputs := make([]featureInput, 0)
	for rows.Next() {
		var item featureInput
		if err := rows.Scan(&item.id, &item.key, &item.valueType); err != nil {
			rows.Close()
			platformResult(c, fmt.Errorf("could not read feature catalog"), "")
			return
		}
		item.value = c.PostForm("feature_" + item.key)
		if item.valueType == "boolean" && item.value == "" {
			item.value = "false"
		}
		if !validFeatureValue(item.valueType, item.value) {
			rows.Close()
			platformResult(c, fmt.Errorf("invalid value for feature %s", item.key), "")
			return
		}
		inputs = append(inputs, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		platformResult(c, fmt.Errorf("could not read feature catalog"), "")
		return
	}
	rows.Close()
	for _, input := range inputs {
		_, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO plan_features (plan_id, feature_id, value) VALUES ($1, $2, $3)
			ON CONFLICT (plan_id, feature_id) DO UPDATE SET value = EXCLUDED.value
		`, c.Param("id"), input.id, input.value)
		if err != nil {
			platformResult(c, fmt.Errorf("could not update plan features"), "")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		platformResult(c, fmt.Errorf("could not save plan features"), "")
		return
	}
	platformResult(c, nil, "Plan features updated")
}

func SetAccountFeatureOverride(c *gin.Context) {
	admin, ok := superadmin(c)
	if !ok {
		return
	}
	accountID := c.PostForm("account_id")
	featureID := c.PostForm("feature_id")
	value := strings.TrimSpace(c.PostForm("value"))
	reason := strings.TrimSpace(c.PostForm("reason"))
	if accountID == "" || featureID == "" || reason == "" {
		platformResult(c, fmt.Errorf("account, feature, value, and reason are required"), "")
		return
	}
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var valueType string
		if err := tx.QueryRowContext(c.Request.Context(), "SELECT value_type FROM features WHERE id = $1", featureID).Scan(&valueType); err != nil {
			return fmt.Errorf("feature not found")
		}
		if !validFeatureValue(valueType, value) {
			return fmt.Errorf("value does not match feature type")
		}
		_, err := tx.ExecContext(c.Request.Context(), `
			INSERT INTO account_feature_overrides (account_id, feature_id, value, reason, granted_by)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (account_id, feature_id) DO UPDATE SET value = EXCLUDED.value,
			reason = EXCLUDED.reason, granted_by = EXCLUDED.granted_by, created_at = now()
		`, accountID, featureID, value, reason, admin.ID)
		return err
	})
	platformResult(c, err, "Account feature override saved")
}

func DeleteAccountFeatureOverride(c *gin.Context) {
	if _, ok := superadmin(c); !ok {
		return
	}
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(c.Request.Context(), "DELETE FROM account_feature_overrides WHERE id = $1", c.Param("id"))
		return err
	})
	platformResult(c, err, "Account feature override removed")
}

func UpdateAccountSubscription(c *gin.Context) {
	if _, ok := superadmin(c); !ok {
		return
	}
	planID := c.PostForm("plan_id")
	status := c.PostForm("status")
	start, errStart := time.Parse("2006-01-02", c.PostForm("period_start"))
	end, errEnd := time.Parse("2006-01-02", c.PostForm("period_end"))
	if planID == "" || (status != "trialing" && status != "active" && status != "past_due" && status != "canceled") || errStart != nil || errEnd != nil || !end.After(start) {
		platformResult(c, fmt.Errorf("select a plan, valid status, and valid period dates"), "")
		return
	}
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var existingID string
		err := tx.QueryRowContext(c.Request.Context(), `
			SELECT id FROM account_subscriptions WHERE account_id = $1
			AND status IN ('trialing', 'active', 'past_due') FOR UPDATE
		`, c.Param("id")).Scan(&existingID)
		if err == nil {
			_, err = tx.ExecContext(c.Request.Context(), `
				UPDATE account_subscriptions SET plan_id = $1, status = $2,
				current_period_start = $3, current_period_end = $4, updated_at = now()
				WHERE id = $5
			`, planID, status, start, end, existingID)
			return err
		}
		if err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO account_subscriptions (account_id, plan_id, status, current_period_start, current_period_end)
			VALUES ($1, $2, $3, $4, $5)
		`, c.Param("id"), planID, status, start, end)
		return err
	})
	platformResult(c, err, "Account subscription updated")
}

func parseBDTAmount(value string) (int, error) {
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > float64(math.MaxInt32)/100 {
		return 0, fmt.Errorf("invalid BDT amount")
	}
	return int(math.Round(amount * 100)), nil
}

func validFeatureType(valueType string) bool {
	return valueType == "boolean" || valueType == "number" || valueType == "text"
}

func validFeatureValue(valueType, value string) bool {
	switch valueType {
	case "boolean":
		return value == "true" || value == "false"
	case "number":
		integer, err := strconv.Atoi(value)
		return err == nil && integer >= 0
	case "text":
		return len(value) <= 500
	default:
		return false
	}
}
