package main

import (
	"bufio"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"restaurant-saas/internal/auth"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/handlers"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// loadDotEnv loads environment variables from a .env file if present
func loadDotEnv(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {
		return // File doesn't exist, proceed with environment variables
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

func main() {
	// 0. Load .env file if available (for local dev)
	loadDotEnv(".env")

	// 1. Resolve environment variables
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		// Fallback to local default database
		dbURL = "postgres://postgres:devpass@localhost:5432/rms_dev?sslmode=disable"
	}

	// 2. Initialize Database pool
	log.Println("Connecting to database...")
	err := db.InitDB(dbURL)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer db.DB.Close()

	// 3. Run database schema migrations
	log.Println("Running schema migrations...")
	// The migrations are stored inside docs/ directory
	err = db.RunMigrations("docs")
	if err != nil {
		log.Fatalf("Migration runner failed: %v", err)
	}
	log.Println("Schema migrations completed successfully.")

	// Run seed fixtures on dev setup if table is empty
	var userCount int
	err = db.DB.QueryRow("SELECT count(*) FROM users").Scan(&userCount)
	if err == nil && userCount == 0 {
		log.Println("Database is empty, loading reference seed fixtures...")
		seedContent, err := os.ReadFile("docs/seed.sql")
		if err == nil {
			_, err = db.DB.Exec(string(seedContent))
			if err != nil {
				log.Printf("Warning: failed to execute seed.sql: %v", err)
			} else {
				log.Println("Database successfully seeded.")
			}
		} else {
			log.Printf("Warning: failed to read seed.sql: %v", err)
		}
	}

	// Initialize sessions secret
	auth.InitSession()

	// 4. Stand up Gin Engine
	r := gin.Default()

	// Configure template functions helper mapping
	r.SetFuncMap(template.FuncMap{
		"formatPrice": func(poisha int) string {
			return fmt.Sprintf("%.2f", float64(poisha)/100.0)
		},
		"formatPriceInt": func(poisha int) string {
			return fmt.Sprintf("%.0f", float64(poisha)/100.0)
		},
		"multiply": func(qty int, price int) int {
			return qty * price
		},
		"formatTime": func(t time.Time) string {
			return t.Format("02 Jan 2006, 03:04 PM")
		},
		"formatFloat": func(f *float64) string {
			if f == nil {
				return "--"
			}
			return fmt.Sprintf("%.2f", *f)
		},
		"formatQty": func(q float64) string {
			return fmt.Sprintf("%.2f", q)
		},
		"isLowStock": func(curr float64, thresh *float64) bool {
			if thresh == nil {
				return false
			}
			return curr <= *thresh
		},
		"isOutOfStock": func(curr float64) bool {
			return curr <= 0.0
		},
		"formatUnitCost": func(cost *int) string {
			if cost == nil {
				return ""
			}
			return fmt.Sprintf("%.2f", float64(*cost)/100.0)
		},
		"isEqualStringPtr": func(s string, ptr *string) bool {
			if ptr == nil {
				return false
			}
			return s == *ptr
		},
	})

	// Load HTML templates
	templates, err := filepath.Glob("templates/*.tmpl")
	if err != nil {
		log.Fatalf("Glob templates failed: %v", err)
	}
	components, err := filepath.Glob("templates/components/*.tmpl")
	if err != nil {
		log.Fatalf("Glob components failed: %v", err)
	}
	templates = append(templates, components...)
	r.LoadHTMLFiles(templates...)

	// Serve static files
	r.Static("/static", "./static")

	// 5. Routing Definitions
	r.GET("/login", handlers.ShowLogin)
	r.POST("/login", handlers.HandleLogin)
	r.POST("/logout", handlers.HandleLogout)

	// Authenticated routes
	authGroup := r.Group("/")
	authGroup.Use(handlers.RequireAuth())
	{
		authGroup.GET("/", handlers.ShowDashboard)
		authGroup.POST("/switch-restaurant", handlers.SwitchRestaurant)

		// Tables
		authGroup.GET("/tables", handlers.ShowTables)
		authGroup.POST("/tables/add", handlers.AddTable)
		authGroup.POST("/tables/:id/status", handlers.UpdateTableStatus)

		// Menu Management
		authGroup.GET("/menu", handlers.ShowMenu)
		authGroup.POST("/menu/categories/add", handlers.AddCategory)
		authGroup.POST("/menu/categories/delete/:id", handlers.DeleteCategory)
		authGroup.POST("/menu/items/add", handlers.AddMenuItem)
		authGroup.POST("/menu/items/delete/:id", handlers.DeleteMenuItem)

		// Orders & POS
		authGroup.GET("/orders", handlers.ShowOrdersLists)
		authGroup.POST("/orders/create", handlers.CreateOrder)
		authGroup.GET("/orders/:id", handlers.ShowOrderDetails)
		authGroup.POST("/orders/:id/items/add", handlers.AddOrderItem)
		authGroup.POST("/orders/:id/items/:item_id/qty", handlers.UpdateItemQty)
		authGroup.POST("/orders/:id/items/:item_id/override", handlers.OverrideItemPrice)
		authGroup.POST("/orders/:id/close", handlers.CloseOrder)

		// Mock thermal receipts
		authGroup.GET("/invoices/:id/pdf", handlers.ShowMockInvoice)

		// Inventory Management
		authGroup.GET("/inventory", handlers.ShowInventory)
		authGroup.POST("/inventory/items/add", handlers.AddInventoryItem)
		authGroup.POST("/inventory/adjustments/add", handlers.AdjustInventoryStock)
		authGroup.POST("/inventory/items/delete/:id", handlers.DeleteInventoryItem)

		// Reports
		authGroup.GET("/reports", handlers.ShowReports)
		authGroup.GET("/reports/export", handlers.ExportReportsCSV)

		// Billing & upgrades
		authGroup.GET("/billing", handlers.ShowBilling)
		authGroup.POST("/billing/checkout", handlers.TriggerMockCheckout)
	}

	// 6. Listen and Serve
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Server starting on http://0.0.0.0:%s", port)
	if err := r.Run("0.0.0.0:" + port); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}
