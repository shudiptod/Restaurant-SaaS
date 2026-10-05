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
		"subtract": func(value int, deduction int) int {
			return value - deduction
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
		authGroup.GET("/restaurants/new", ShowNewRestaurant)
		authGroup.POST("/restaurants", CreateRestaurant)
		authGroup.GET("/team", ShowTeam)
		authGroup.POST("/team/create", CreateRestaurantLogin)
		authGroup.POST("/team/add-existing", AddExistingRestaurantLogin)
		authGroup.GET("/tables", ShowTables)
		authGroup.POST("/tables/:id/status", UpdateTableStatus)
		authGroup.POST("/tables/add", AddTable)
		authGroup.GET("/staff", ShowStaff)
		authGroup.POST("/staff/add", AddRestaurantStaff)
		authGroup.POST("/staff/:id/status", UpdateRestaurantStaffStatus)

		authGroup.GET("/orders", ShowOrdersLists)
		authGroup.POST("/orders/create", CreateOrder)
		authGroup.GET("/orders/:id", ShowOrderDetails)
		authGroup.POST("/orders/:id/items/add", AddOrderItem)
		authGroup.POST("/orders/:id/items/:item_id/qty", UpdateItemQty)
		authGroup.POST("/orders/:id/items/:item_id/override", OverrideItemPrice)
		authGroup.POST("/orders/:id/staff", SetOrderStaff)
		authGroup.POST("/orders/:id/discount", SetOrderDiscount)
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

func TestCreateOrderResumesOpenTableOrder(t *testing.T) {
	r := setupTestRouter(t)
	unique := fmt.Sprintf("resume-%d", time.Now().UnixNano())
	var userID, accountID, restaurantID, tableID, menuItemID string
	if err := db.DB.QueryRow(`
		INSERT INTO users (email, password_hash, full_name)
		VALUES ($1, 'test-hash', 'Resume Test') RETURNING id
	`, unique+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	if err := db.DB.QueryRow(`
		INSERT INTO accounts (name, owner_user_id) VALUES ($1, $2) RETURNING id
	`, unique, userID).Scan(&accountID); err != nil {
		t.Fatalf("create test account: %v", err)
	}
	if err := db.DB.QueryRow(`
		INSERT INTO restaurants (account_id, name, slug) VALUES ($1, $2, $3) RETURNING id
	`, accountID, unique, unique).Scan(&restaurantID); err != nil {
		t.Fatalf("create test restaurant: %v", err)
	}
	if _, err := db.DB.Exec(`
		INSERT INTO restaurant_users (restaurant_id, user_id, role) VALUES ($1, $2, 'owner')
	`, restaurantID, userID); err != nil {
		t.Fatalf("create test membership: %v", err)
	}
	if err := db.DB.QueryRow(`
		INSERT INTO tables (restaurant_id, name, capacity, status)
		VALUES ($1, $2, 4, 'occupied') RETURNING id
	`, restaurantID, unique).Scan(&tableID); err != nil {
		t.Fatalf("create occupied test table: %v", err)
	}
	if err := db.DB.QueryRow(`
		INSERT INTO menu_items (restaurant_id, name, price) VALUES ($1, $2, 500) RETURNING id
	`, restaurantID, unique+" item").Scan(&menuItemID); err != nil {
		t.Fatalf("create test menu item: %v", err)
	}
	defer func() {
		_, _ = db.DB.Exec("DELETE FROM orders WHERE table_id = $1", tableID)
		_, _ = db.DB.Exec("DELETE FROM menu_items WHERE id = $1", menuItemID)
		_, _ = db.DB.Exec("DELETE FROM accounts WHERE id = $1", accountID)
		_, _ = db.DB.Exec("DELETE FROM users WHERE id = $1", userID)
	}()
	loginResponse := httptest.NewRecorder()
	auth.SetSessionCookie(loginResponse, userID)
	var cookie *http.Cookie
	for _, candidate := range loginResponse.Result().Cookies() {
		if candidate.Name == auth.CookieName {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("test session did not issue an auth cookie")
	}

	postOrder := func() *httptest.ResponseRecorder {
		form := url.Values{"table_id": {tableID}}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/orders/create", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: "rms_active_restaurant", Value: restaurantID})
		r.ServeHTTP(w, req)
		return w
	}

	first := postOrder()
	if first.Code != http.StatusSeeOther {
		t.Fatalf("expected first order redirect, got %d: %s", first.Code, first.Body.String())
	}
	orderID := strings.TrimPrefix(first.Header().Get("Location"), "/orders/")
	if _, err := db.DB.Exec(`
		INSERT INTO order_items (order_id, menu_item_id, quantity, unit_price) VALUES ($1, $2, 1, 500)
	`, orderID, menuItemID); err != nil {
		t.Fatalf("add existing line to open order: %v", err)
	}
	second := postOrder()
	if second.Code != http.StatusSeeOther {
		t.Fatalf("expected resumed order redirect, got %d: %s", second.Code, second.Body.String())
	}
	if first.Header().Get("Location") != second.Header().Get("Location") {
		t.Fatalf("expected same order to resume, got %q then %q", first.Header().Get("Location"), second.Header().Get("Location"))
	}

	var openCount int
	if err := db.DB.QueryRow("SELECT count(*) FROM orders WHERE table_id = $1 AND status = 'open'", tableID).Scan(&openCount); err != nil {
		t.Fatalf("count table orders: %v", err)
	}
	if openCount != 1 {
		t.Fatalf("expected one open order for table, got %d", openCount)
	}
	var itemCount int
	if err := db.DB.QueryRow("SELECT count(*) FROM order_items WHERE order_id = $1", orderID).Scan(&itemCount); err != nil {
		t.Fatalf("count persisted order items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("expected the existing order line to persist, got %d lines", itemCount)
	}

	ordersPage := httptest.NewRecorder()
	ordersRequest, _ := http.NewRequest("GET", "/orders", nil)
	ordersRequest.AddCookie(cookie)
	ordersRequest.AddCookie(&http.Cookie{Name: "rms_active_restaurant", Value: restaurantID})
	r.ServeHTTP(ordersPage, ordersRequest)
	if ordersPage.Code != http.StatusOK || !strings.Contains(ordersPage.Body.String(), unique+" (4 pax, occupied)") {
		t.Fatalf("occupied table was not available in POS order selection: %d", ordersPage.Code)
	}

	tablesPage := httptest.NewRecorder()
	tablesRequest, _ := http.NewRequest("GET", "/tables", nil)
	tablesRequest.AddCookie(cookie)
	tablesRequest.AddCookie(&http.Cookie{Name: "rms_active_restaurant", Value: restaurantID})
	r.ServeHTTP(tablesPage, tablesRequest)
	if tablesPage.Code != http.StatusOK || !strings.Contains(tablesPage.Body.String(), `action="/orders/create"`) || !strings.Contains(tablesPage.Body.String(), `data-elapsed-from=`) {
		t.Fatalf("table page is missing direct POS action or active-order timer: %d", tablesPage.Code)
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
