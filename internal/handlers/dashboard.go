package handlers

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"
	"time"

	"github.com/gin-gonic/gin"
)

// DashboardStats holds numbers-forward counts for owner overview
type DashboardStats struct {
	TodaySales     int
	OpenOrders     int
	TotalTables    int
	OccupiedTables int
	OccupiedPct    float64
	LowStockCount  int
}

// ShowDashboard renders owner landing page
func ShowDashboard(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	var stats DashboardStats
	var restName string
	var hasConsolidatedReports bool

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		// Load active restaurant context details
		var accountID string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT name, account_id FROM restaurants WHERE id = $1", activeRestID).Scan(&restName, &accountID)
		if err != nil {
			return err
		}

		// 1. Verify consolidated reporting access
		limit, err := GetLimit(c.Request.Context(), accountID, "consolidated_reports")
		if err == nil && limit == 1 {
			hasConsolidatedReports = true
		}

		// 2. Fetch stats
		todayStart := time.Now().Truncate(24 * time.Hour)
		todayEnd := todayStart.Add(24 * time.Hour)

		// Today's Sales
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT COALESCE(SUM(total_amount), 0) FROM orders
			WHERE restaurant_id = $1 AND status = 'closed' AND closed_at >= $2 AND closed_at < $3
		`, activeRestID, todayStart, todayEnd).Scan(&stats.TodaySales)
		if err != nil {
			return err
		}

		// Open Orders Count
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT count(*) FROM orders WHERE restaurant_id = $1 AND status = 'open'
		`, activeRestID).Scan(&stats.OpenOrders)
		if err != nil {
			return err
		}

		// Table occupancy details
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT count(*) FROM tables WHERE restaurant_id = $1
		`, activeRestID).Scan(&stats.TotalTables)
		if err != nil {
			return err
		}

		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT count(*) FROM tables WHERE restaurant_id = $1 AND status = 'occupied'
		`, activeRestID).Scan(&stats.OccupiedTables)
		if err != nil {
			return err
		}

		if stats.TotalTables > 0 {
			stats.OccupiedPct = (float64(stats.OccupiedTables) / float64(stats.TotalTables)) * 100
		}

		// Low stock count
		_ = tx.QueryRowContext(c.Request.Context(), `
			SELECT count(*) FROM inventory_items
			WHERE restaurant_id = $1 AND deleted_at IS NULL AND reorder_threshold IS NOT NULL AND current_quantity <= reorder_threshold
		`, activeRestID).Scan(&stats.LowStockCount)

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load dashboard: "+err.Error())
		return
	}

	c.HTML(http.StatusOK, "dashboard.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"RestaurantName":     restName,
		"ActiveNav":          "dashboard",
		"Stats":              stats,
		"Consolidated":       hasConsolidatedReports,
	})
}

type ReportItem struct {
	Name     string
	Quantity int
	Amount   int
}

type PaymentBreakdown struct {
	Method string
	Amount int
}

type DiscountAudit struct {
	CreatedAt time.Time
	User      string
	Item      string
	Discount  int
	Reason    string
}

// ShowReports renders performance analytics reports with filters
func ShowReports(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate time.Time
	if startDateStr != "" {
		startDate, _ = time.Parse("2006-01-02", startDateStr)
	} else {
		startDate = time.Now().AddDate(0, 0, -30) // Default 30 days
	}
	if endDateStr != "" {
		endDate, _ = time.Parse("2006-01-02", endDateStr)
		endDate = endDate.Add(24 * time.Hour) // Include the full end date day
	} else {
		endDate = time.Now().Add(24 * time.Hour)
	}

	var totalRevenue int
	var discountsGiven int
	var topItems []ReportItem
	var salesByCategory []ReportItem
	var paymentMethods []PaymentBreakdown
	var discountAudits []DiscountAudit
	var inventoryItems []models.InventoryItem
	var canExportData bool

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var accountID string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT account_id FROM restaurants WHERE id = $1", activeRestID).Scan(&accountID)
		if err == nil {
			limit, err := GetLimit(c.Request.Context(), accountID, "data_export")
			if err == nil && limit == 1 {
				canExportData = true
			}
		}

		// Total Revenue
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT COALESCE(SUM(total_amount), 0) FROM orders
			WHERE restaurant_id = $1 AND status = 'closed' AND closed_at >= $2 AND closed_at < $3
		`, activeRestID, startDate, endDate).Scan(&totalRevenue)
		if err != nil {
			return err
		}

		// Total Discount Amount Given
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT COALESCE(SUM(discount_amount), 0) FROM orders
			WHERE restaurant_id = $1 AND status = 'closed' AND closed_at >= $2 AND closed_at < $3
		`, activeRestID, startDate, endDate).Scan(&discountsGiven)
		if err != nil {
			return err
		}

		// Top Items
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT mi.name, SUM(oi.quantity), SUM(oi.quantity * oi.unit_price)
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			JOIN orders o ON o.id = oi.order_id
			WHERE o.restaurant_id = $1 AND o.status = 'closed' AND o.closed_at >= $2 AND o.closed_at < $3
			GROUP BY mi.name
			ORDER BY SUM(oi.quantity) DESC
			LIMIT 5
		`, activeRestID, startDate, endDate)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var ri ReportItem
				if err := rows.Scan(&ri.Name, &ri.Quantity, &ri.Amount); err == nil {
					topItems = append(topItems, ri)
				}
			}
		}

		// Sales by Category
		catRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT mc.name, COALESCE(SUM(oi.quantity), 0), COALESCE(SUM(oi.quantity * oi.unit_price), 0)
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			JOIN menu_categories mc ON mc.id = mi.category_id
			JOIN orders o ON o.id = oi.order_id
			WHERE o.restaurant_id = $1 AND o.status = 'closed' AND o.closed_at >= $2 AND o.closed_at < $3
			GROUP BY mc.name
			ORDER BY SUM(oi.quantity * oi.unit_price) DESC
		`, activeRestID, startDate, endDate)
		if err == nil {
			defer catRows.Close()
			for catRows.Next() {
				var ri ReportItem
				if err := catRows.Scan(&ri.Name, &ri.Quantity, &ri.Amount); err == nil {
					salesByCategory = append(salesByCategory, ri)
				}
			}
		}

		// Payments breakdown
		payRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT method, SUM(amount) FROM order_payments
			WHERE restaurant_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY method
		`, activeRestID, startDate, endDate)
		if err == nil {
			defer payRows.Close()
			for payRows.Next() {
				var pb PaymentBreakdown
				if err := payRows.Scan(&pb.Method, &pb.Amount); err == nil {
					paymentMethods = append(paymentMethods, pb)
				}
			}
		}

		// Discount/Override Audits
		audRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT a.created_at, u.full_name, mi.name, (a.original_price - a.adjusted_price) * oi.quantity, a.reason
			FROM order_item_price_adjustments a
			JOIN users u ON u.id = a.adjusted_by
			JOIN order_items oi ON oi.id = a.order_item_id
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			WHERE a.restaurant_id = $1 AND a.created_at >= $2 AND a.created_at < $3
			ORDER BY a.created_at DESC
		`, activeRestID, startDate, endDate)
		if err == nil {
			defer audRows.Close()
			for audRows.Next() {
				var da DiscountAudit
				if err := audRows.Scan(&da.CreatedAt, &da.User, &da.Item, &da.Discount, &da.Reason); err == nil {
					discountAudits = append(discountAudits, da)
				}
			}
		}

		// Inventory Levels for report
		invRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT id, name, unit, current_quantity, reorder_threshold
			FROM inventory_items
			WHERE restaurant_id = $1 AND deleted_at IS NULL
			ORDER BY name ASC
		`, activeRestID)
		if err == nil {
			defer invRows.Close()
			for invRows.Next() {
				var ii models.InventoryItem
				var thresh sql.NullFloat64
				if err := invRows.Scan(&ii.ID, &ii.Name, &ii.Unit, &ii.CurrentQuantity, &thresh); err == nil {
					if thresh.Valid {
						ii.ReorderThreshold = &thresh.Float64
					}
					inventoryItems = append(inventoryItems, ii)
				}
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// Format display dates
	formattedStart := startDate.Format("2006-01-02")
	formattedEnd := endDate.Add(-24 * time.Hour).Format("2006-01-02")

	// Render HTMX fragment if requested
	if c.GetHeader("HX-Request") == "true" {
		c.HTML(http.StatusOK, "reports_fragment", gin.H{
			"TotalRevenue":    totalRevenue,
			"DiscountsGiven":  discountsGiven,
			"TopItems":        topItems,
			"SalesByCategory": salesByCategory,
			"PaymentMethods":  paymentMethods,
			"DiscountAudits":  discountAudits,
			"InventoryItems":  inventoryItems,
			"CanExportData":   canExportData,
			"StartDate":       formattedStart,
			"EndDate":         formattedEnd,
		})
		return
	}

	c.HTML(http.StatusOK, "reports.tmpl", gin.H{
		"User":               user,
		"ActiveRestaurantID": activeRestID,
		"ActiveNav":          "reports",
		"TotalRevenue":       totalRevenue,
		"DiscountsGiven":     discountsGiven,
		"TopItems":           topItems,
		"SalesByCategory":    salesByCategory,
		"PaymentMethods":     paymentMethods,
		"DiscountAudits":     discountAudits,
		"InventoryItems":     inventoryItems,
		"CanExportData":      canExportData,
		"StartDate":          formattedStart,
		"EndDate":            formattedEnd,
	})
}

// ExportReportsCSV exports filtered sales and inventory reports to a CSV file
func ExportReportsCSV(c *gin.Context) {
	val, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := val.(CurrentUser)
	activeRestID := GetActiveRestaurantID(c, user)

	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	var startDate, endDate time.Time
	if startDateStr != "" {
		startDate, _ = time.Parse("2006-01-02", startDateStr)
	} else {
		startDate = time.Now().AddDate(0, 0, -30)
	}
	if endDateStr != "" {
		endDate, _ = time.Parse("2006-01-02", endDateStr)
		endDate = endDate.Add(24 * time.Hour)
	} else {
		endDate = time.Now().Add(24 * time.Hour)
	}

	var totalRevenue int
	var discountsGiven int
	var topItems []ReportItem
	var salesByCategory []ReportItem
	var paymentMethods []PaymentBreakdown

	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var accountID string
		err := tx.QueryRowContext(c.Request.Context(), "SELECT account_id FROM restaurants WHERE id = $1", activeRestID).Scan(&accountID)
		if err != nil {
			return err
		}

		limit, err := GetLimit(c.Request.Context(), accountID, "data_export")
		if err != nil || limit == 0 {
			return fmt.Errorf("data export feature is not enabled for your current subscription plan")
		}

		// Total Revenue
		_ = tx.QueryRowContext(c.Request.Context(), `
			SELECT COALESCE(SUM(total_amount), 0) FROM orders
			WHERE restaurant_id = $1 AND status = 'closed' AND closed_at >= $2 AND closed_at < $3
		`, activeRestID, startDate, endDate).Scan(&totalRevenue)

		// Discounts
		_ = tx.QueryRowContext(c.Request.Context(), `
			SELECT COALESCE(SUM(discount_amount), 0) FROM orders
			WHERE restaurant_id = $1 AND status = 'closed' AND closed_at >= $2 AND closed_at < $3
		`, activeRestID, startDate, endDate).Scan(&discountsGiven)

		// Top Items
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT mi.name, SUM(oi.quantity), SUM(oi.quantity * oi.unit_price)
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			JOIN orders o ON o.id = oi.order_id
			WHERE o.restaurant_id = $1 AND o.status = 'closed' AND o.closed_at >= $2 AND o.closed_at < $3
			GROUP BY mi.name
			ORDER BY SUM(oi.quantity) DESC
		`, activeRestID, startDate, endDate)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var ri ReportItem
				if err := rows.Scan(&ri.Name, &ri.Quantity, &ri.Amount); err == nil {
					topItems = append(topItems, ri)
				}
			}
		}

		// Category sales
		catRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT mc.name, COALESCE(SUM(oi.quantity), 0), COALESCE(SUM(oi.quantity * oi.unit_price), 0)
			FROM order_items oi
			JOIN menu_items mi ON mi.id = oi.menu_item_id
			JOIN menu_categories mc ON mc.id = mi.category_id
			JOIN orders o ON o.id = oi.order_id
			WHERE o.restaurant_id = $1 AND o.status = 'closed' AND o.closed_at >= $2 AND o.closed_at < $3
			GROUP BY mc.name
			ORDER BY SUM(oi.quantity * oi.unit_price) DESC
		`, activeRestID, startDate, endDate)
		if err == nil {
			defer catRows.Close()
			for catRows.Next() {
				var ri ReportItem
				if err := catRows.Scan(&ri.Name, &ri.Quantity, &ri.Amount); err == nil {
					salesByCategory = append(salesByCategory, ri)
				}
			}
		}

		// Payment methods
		payRows, err := tx.QueryContext(c.Request.Context(), `
			SELECT method, SUM(amount) FROM order_payments
			WHERE restaurant_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY method
		`, activeRestID, startDate, endDate)
		if err == nil {
			defer payRows.Close()
			for payRows.Next() {
				var pb PaymentBreakdown
				if err := payRows.Scan(&pb.Method, &pb.Amount); err == nil {
					paymentMethods = append(paymentMethods, pb)
				}
			}
		}

		return nil
	})

	if err != nil {
		c.String(http.StatusForbidden, err.Error())
		return
	}

	buf := &bytes.Buffer{}
	writer := csv.NewWriter(buf)

	// Header Summary
	writer.Write([]string{"Restaurant Management SaaS - Performance Analytics Export"})
	writer.Write([]string{"Start Date", startDate.Format("2006-01-02"), "End Date", endDate.Add(-24 * time.Hour).Format("2006-01-02")})
	writer.Write([]string{})

	writer.Write([]string{"Metric", "Value (BDT)"})
	writer.Write([]string{"Total Revenue", fmt.Sprintf("%.2f", float64(totalRevenue)/100.0)})
	writer.Write([]string{"Total Discounts Logged", fmt.Sprintf("%.2f", float64(discountsGiven)/100.0)})
	writer.Write([]string{})

	// Top items
	writer.Write([]string{"Top Selling Items", "Quantity Sold", "Revenue (BDT)"})
	for _, item := range topItems {
		writer.Write([]string{item.Name, fmt.Sprintf("%d", item.Quantity), fmt.Sprintf("%.2f", float64(item.Amount)/100.0)})
	}
	writer.Write([]string{})

	// Sales by Category
	writer.Write([]string{"Sales By Category", "Total Items Sold", "Revenue (BDT)"})
	for _, cat := range salesByCategory {
		writer.Write([]string{cat.Name, fmt.Sprintf("%d", cat.Quantity), fmt.Sprintf("%.2f", float64(cat.Amount)/100.0)})
	}
	writer.Write([]string{})

	// Payment Breakdown
	writer.Write([]string{"Payment Method", "Total Collected (BDT)"})
	for _, pm := range paymentMethods {
		writer.Write([]string{pm.Method, fmt.Sprintf("%.2f", float64(pm.Amount)/100.0)})
	}

	writer.Flush()

	filename := fmt.Sprintf("report_%s_%s.csv", startDate.Format("2006-01-02"), endDate.Add(-24*time.Hour).Format("2006-01-02"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
}
