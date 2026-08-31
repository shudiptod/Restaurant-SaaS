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

type SubscriptionView struct {
	PlanName      string
	PlanCode      string
	Status        string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	LimitTables   int
	LimitRest     int
	LimitUsers    int
}

// ShowBilling renders the account subscription and payments history
func ShowBilling(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	var subView SubscriptionView
	var paymentsList []models.Invoice // We'll map payment logs here
	var plans []models.Restaurant    // Placeholder list of plans to buy

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Load Account ID
		var accountID string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT account_id FROM restaurants WHERE id = $1", activeRestID).Scan(&accountID)
		if err != nil {
			return err
		}

		// Load Subscription
		var planName, planCode, subStatus string
		var start, end time.Time
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT p.name, p.code, s.status, s.current_period_start, s.current_period_end
			FROM account_subscriptions s
			JOIN subscription_plans p ON p.id = s.plan_id
			WHERE s.account_id = $1 AND s.status IN ('trialing','active','past_due','canceled')
			LIMIT 1
		`, accountID).Scan(&planName, &planCode, &subStatus, &start, &end)
		if err != nil && err != sql.ErrNoRows {
			return err
		}

		subView.PlanName = planName
		subView.PlanCode = planCode
		subView.Status = subStatus
		subView.PeriodStart = start
		subView.PeriodEnd = end

		// Load plan limits
		subView.LimitTables, _ = GetLimit(c.Request.Context(), accountID, "max_tables_per_restaurant")
		subView.LimitRest, _ = GetLimit(c.Request.Context(), accountID, "max_restaurants")
		subView.LimitUsers, _ = GetLimit(c.Request.Context(), accountID, "max_users_per_account")

		// Load subscription payment history
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, amount, status, provider_trx_id, created_at
			FROM payments WHERE account_id = $1 ORDER BY created_at DESC
		`, accountID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var p models.Invoice // mapping payments to invoice struct for display convenience
				var trx sql.NullString
				var status string
				var crTime time.Time
				if err := rows.Scan(&p.ID, &p.TotalAmount, &status, &trx, &crTime); err == nil {
					p.CreatedAt = crTime
					p.InvoiceNumber = 0 // represents sub payment
					if trx.Valid {
						p.PdfURL = &trx.String // store trxID in PDF for representation
					}
					// Map payment status text to subtotal/PDF string
					p.Subtotal = 0 // we will display status on UI using Subtotal mapping or logic
					if status == "completed" {
						p.Subtotal = 1 // marks success
					}
					paymentsList = append(paymentsList, p)
				}
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	c.HTML(http.StatusOK, "billing.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"ActiveNav":          "billing",
		"Subscription":       subView,
		"Payments":           paymentsList,
		"Plans":              plans,
	})
}

// TriggerMockCheckout mock bKash checkout that extends the subscription billing period
func TriggerMockCheckout(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)
	planCode := c.PostForm("plan_code") // "basic", "pro", "enterprise"

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Fetch Account
		var accountID string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT account_id FROM restaurants WHERE id = $1", activeRestID).Scan(&accountID)
		if err != nil {
			return err
		}

		// Find target plan details
		var planID string
		var price int
		err = tx.QueryRowContext(c.Request.Context(), "SELECT id, price_amount FROM subscription_plans WHERE code = $1", planCode).Scan(&planID, &price)
		if err != nil {
			return err
		}

		// 1. Create a successful completed payment simulation row
		trxID := "TRX-BKASH-" + strconv.FormatInt(time.Now().Unix(), 10)
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO payments (account_id, amount, provider, provider_payment_id, provider_trx_id, status, paid_at)
			VALUES ($1, $2, 'bkash', $3, $4, 'completed', $5)
		`, accountID, price, "PAY-MOCK-"+trxID, trxID, time.Now())
		if err != nil {
			return err
		}

		// 2. Extend subscription
		now := time.Now()
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO account_subscriptions (account_id, plan_id, status, current_period_start, current_period_end)
			VALUES ($1, $2, 'active', $3, $4)
			ON CONFLICT (account_id)
			DO UPDATE SET plan_id = EXCLUDED.plan_id, status = 'active', 
			              current_period_start = EXCLUDED.current_period_start, 
			              current_period_end = EXCLUDED.current_period_end
		`, accountID, planID, now, now.AddDate(0, 1, 0)) // +30 days
		return err
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	c.Redirect(http.StatusSeeOther, "/billing")
}
