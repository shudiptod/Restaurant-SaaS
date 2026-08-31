package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"restaurant-saas/internal/auth"
	"restaurant-saas/internal/db"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupTestRouter(t *testing.T) *gin.Engine {
	gin.SetMode(gin.TestMode)
	dbURL := "postgres://postgres:devpass@localhost:5432/rms_dev?sslmode=disable"
	err := db.InitDB(dbURL)
	if err != nil {
		t.Fatalf("Database connection failed: %v", err)
	}

	auth.InitSession()

	r := gin.New()
	r.Use(gin.Recovery())

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

	templates, err := filepath.Glob("../../templates/*.tmpl")
	if err != nil {
		t.Fatalf("Glob templates failed: %v", err)
	}
	components, err := filepath.Glob("../../templates/components/*.tmpl")
	if err != nil {
		t.Fatalf("Glob components failed: %v", err)
	}
	templates = append(templates, components...)
	r.LoadHTMLFiles(templates...)

	r.POST("/login", HandleLogin)

	authGroup := r.Group("/")
	authGroup.Use(RequireAuth())
	{
		authGroup.GET("/", ShowDashboard)
		authGroup.GET("/tables", ShowTables)
		authGroup.POST("/tables/:id/status", UpdateTableStatus)
		authGroup.POST("/tables/add", AddTable)

		authGroup.GET("/orders", ShowOrdersLists)
		authGroup.POST("/orders/create", CreateOrder)
		authGroup.GET("/orders/:id", ShowOrderDetails)
		authGroup.POST("/orders/:id/items/add", AddOrderItem)
		authGroup.POST("/orders/:id/items/:item_id/qty", UpdateItemQty)
		authGroup.POST("/orders/:id/items/:item_id/override", OverrideItemPrice)
		authGroup.POST("/orders/:id/close", CloseOrder)

		authGroup.GET("/inventory", ShowInventory)
		authGroup.POST("/inventory/items/add", AddInventoryItem)
		authGroup.POST("/inventory/adjustments/add", AdjustInventoryStock)
		authGroup.POST("/inventory/items/delete/:id", DeleteInventoryItem)

		authGroup.GET("/reports", ShowReports)
		authGroup.GET("/reports/export", ExportReportsCSV)
	}

	return r
}

func getAuthCookie(t *testing.T, r *gin.Engine) *http.Cookie {
	w := httptest.NewRecorder()
	formData := url.Values{
		"email":    {"owner@example.com"},
		"password": {"password"},
	}
	req, _ := http.NewRequest("POST", "/login", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("Login failed with status %d: %s", w.Code, w.Body.String())
	}

	for _, c := range w.Result().Cookies() {
		if c.Name == "rms_session" {
			return c
		}
	}
	t.Fatalf("No rms_session cookie found")
	return nil
}

func TestTableStatusInteractivity(t *testing.T) {
	r := setupTestRouter(t)
	cookie := getAuthCookie(t, r)

	// 1. Fetch tables page to find a table ID
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/tables", nil)
	req.AddCookie(cookie)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /tables, got %d", w.Code)
	}

	// Let's create a test table if none exists
	var tableID string
	err := db.DB.QueryRow("SELECT id FROM tables LIMIT 1").Scan(&tableID)
	if err != nil {
		// insert test table
		var restID string
		_ = db.DB.QueryRow("SELECT id FROM restaurants LIMIT 1").Scan(&restID)
		_ = db.DB.QueryRow("INSERT INTO tables (restaurant_id, name, capacity, status) VALUES ($1, 'T-Test', 4, 'available') RETURNING id", restID).Scan(&tableID)
	}

	// 2. Trigger HTMX update to 'occupied'
	wOccupy := httptest.NewRecorder()
	reqOccupy, _ := http.NewRequest("POST", fmt.Sprintf("/tables/%s/status?status=occupied", tableID), nil)
	reqOccupy.AddCookie(cookie)
	reqOccupy.Header.Set("HX-Request", "true")
	r.ServeHTTP(wOccupy, reqOccupy)

	if wOccupy.Code != http.StatusOK {
		t.Fatalf("Expected 200 for HTMX occupy, got %d: %s", wOccupy.Code, wOccupy.Body.String())
	}

	occupyResp := wOccupy.Body.String()
	if !strings.Contains(occupyResp, fmt.Sprintf(`id="table-%s"`, tableID)) {
		t.Errorf("Response missing table container id: %s", occupyResp)
	}
	if !strings.Contains(occupyResp, "status-dot-warn") {
		t.Errorf("Expected status-dot-warn (occupied indicator), got: %s", occupyResp)
	}
	if !strings.Contains(occupyResp, "Status: occupied") {
		t.Errorf("Expected 'Status: occupied' in rendered HTML, got: %s", occupyResp)
	}

	// 3. Trigger HTMX update to 'available' (Free)
	wFree := httptest.NewRecorder()
	reqFree, _ := http.NewRequest("POST", fmt.Sprintf("/tables/%s/status?status=available", tableID), nil)
	reqFree.AddCookie(cookie)
	reqFree.Header.Set("HX-Request", "true")
	r.ServeHTTP(wFree, reqFree)

	if wFree.Code != http.StatusOK {
		t.Fatalf("Expected 200 for HTMX free, got %d", wFree.Code)
	}

	freeResp := wFree.Body.String()
	if !strings.Contains(freeResp, "status-dot-good") {
		t.Errorf("Expected status-dot-good (available indicator), got: %s", freeResp)
	}
	if !strings.Contains(freeResp, "Status: available") {
		t.Errorf("Expected 'Status: available' in rendered HTML, got: %s", freeResp)
	}
}

func TestInventoryWorkflow(t *testing.T) {
	r := setupTestRouter(t)
	cookie := getAuthCookie(t, r)

	uniqueItemName := fmt.Sprintf("Organic Spice %d", time.Now().UnixNano())

	// 1. Add new inventory item
	wAdd := httptest.NewRecorder()
	formData := url.Values{
		"name":              {uniqueItemName},
		"unit":              {"kg"},
		"initial_quantity":  {"12.5"},
		"reorder_threshold": {"2.5"},
		"unit_cost":         {"850.00"},
	}
	reqAdd, _ := http.NewRequest("POST", "/inventory/items/add", strings.NewReader(formData.Encode()))
	reqAdd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAdd.AddCookie(cookie)
	r.ServeHTTP(wAdd, reqAdd)

	if wAdd.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 redirect after adding item, got %d: %s", wAdd.Code, wAdd.Body.String())
	}

	// 2. Fetch inventory list
	wList := httptest.NewRecorder()
	reqList, _ := http.NewRequest("GET", "/inventory", nil)
	reqList.AddCookie(cookie)
	r.ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /inventory, got %d", wList.Code)
	}
	listBody := wList.Body.String()
	if !strings.Contains(listBody, uniqueItemName) {
		t.Errorf("Expected '%s' in inventory list, got: %s", uniqueItemName, listBody)
	}
	if !strings.Contains(listBody, "12.50") {
		t.Errorf("Expected '12.50' current stock in inventory list, got: %s", listBody)
	}

	// Find the item ID
	var itemID string
	err := db.DB.QueryRow("SELECT id FROM inventory_items WHERE name = $1 AND deleted_at IS NULL", uniqueItemName).Scan(&itemID)
	if err != nil {
		t.Fatalf("Failed to find created item %s: %v", uniqueItemName, err)
	}

	// 3. Record a usage stock movement (-3.5 kg)
	wAdj := httptest.NewRecorder()
	adjData := url.Values{
		"inventory_item_id": {itemID},
		"change_quantity":   {"3.5"},
		"direction":         {"out"},
		"reason":            {"usage"},
		"note":              {"Saturday buffet prep"},
	}
	reqAdj, _ := http.NewRequest("POST", "/inventory/adjustments/add", strings.NewReader(adjData.Encode()))
	reqAdj.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAdj.AddCookie(cookie)
	r.ServeHTTP(wAdj, reqAdj)

	if wAdj.Code != http.StatusSeeOther {
		t.Fatalf("Expected 303 after adjusting stock, got %d: %s", wAdj.Code, wAdj.Body.String())
	}

	// 4. Verify updated balance (12.5 - 3.5 = 9.00 kg)
	wList2 := httptest.NewRecorder()
	reqList2, _ := http.NewRequest("GET", "/inventory", nil)
	reqList2.AddCookie(cookie)
	r.ServeHTTP(wList2, reqList2)

	list2Body := wList2.Body.String()
	if !strings.Contains(list2Body, "9.00") {
		t.Errorf("Expected '9.00' updated stock in inventory list, got: %s", list2Body)
	}
	if !strings.Contains(list2Body, "Saturday buffet prep") {
		idx := strings.Index(list2Body, "Recent Movements Audit")
		if idx != -1 {
			t.Errorf("Expected adjustment note in movements audit, movements section: %s", list2Body[idx:])
		} else {
			t.Errorf("Recent Movements Audit header not found in body: %s", list2Body)
		}
	}
}

func TestReportsExportCSV(t *testing.T) {
	r := setupTestRouter(t)
	cookie := getAuthCookie(t, r)

	wExp := httptest.NewRecorder()
	reqExp, _ := http.NewRequest("GET", "/reports/export", nil)
	reqExp.AddCookie(cookie)
	r.ServeHTTP(wExp, reqExp)

	if wExp.Code != http.StatusOK {
		t.Fatalf("Expected 200 for /reports/export, got %d: %s", wExp.Code, wExp.Body.String())
	}

	contentType := wExp.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/csv") {
		t.Errorf("Expected Content-Type text/csv, got %s", contentType)
	}

	csvBody := wExp.Body.String()
	if !strings.Contains(csvBody, "Restaurant Management SaaS - Performance Analytics Export") {
		t.Errorf("CSV missing header title: %s", csvBody)
	}
	if !strings.Contains(csvBody, "Total Revenue") {
		t.Errorf("CSV missing Total Revenue metric: %s", csvBody)
	}
}
