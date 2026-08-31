package handlers

import (
	"context"
	"net/http"
	"restaurant-saas/internal/auth"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// CurrentUser represents the logged-in user and their restaurant contexts
type CurrentUser struct {
	ID          string
	Email       string
	FullName    string
	IsPlatformAdmin bool
	PlatformRole string
	Restaurants []models.RestaurantUserContext
}

// RequireAuth middleware extracts session cookie, loads user identity,
// sets up context for Gin and downstream database transactions (RLS).
func RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(auth.CookieName)
		if err != nil {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}

		userID, err := auth.VerifySessionToken(token)
		if err != nil {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}

		// Inject UserID into Go request context for WithTx RLS propagation
		ctx := context.WithValue(c.Request.Context(), db.UserIDKey, userID)
		c.Request = c.Request.WithContext(ctx)

		var user CurrentUser
		user.ID = userID

		// Fetch user details
		err = db.DB.QueryRowContext(ctx, "SELECT email, full_name FROM users WHERE id = $1", userID).
			Scan(&user.Email, &user.FullName)
		if err != nil {
			auth.ClearSessionCookie(c.Writer)
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}

		// Check if platform admin
		var platRole string
		err = db.DB.QueryRowContext(ctx, "SELECT role FROM platform_admins WHERE user_id = $1", userID).Scan(&platRole)
		if err == nil {
			user.IsPlatformAdmin = true
			user.PlatformRole = platRole
		}

		// Fetch active restaurant memberships
		rows, err := db.DB.QueryContext(ctx, `
			SELECT ru.restaurant_id, r.name, ru.role 
			FROM restaurant_users ru
			JOIN restaurants r ON r.id = ru.restaurant_id
			WHERE ru.user_id = $1 AND ru.status = 'active'
		`, userID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var rc models.RestaurantUserContext
				if err := rows.Scan(&rc.RestaurantID, &rc.RestaurantName, &rc.Role); err == nil {
					user.Restaurants = append(user.Restaurants, rc)
				}
			}
		}

		c.Set("user", user)
		c.Next()
	}
}

// ShowLogin renders the login page
func ShowLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "login.tmpl", gin.H{
		"Error": "",
	})
}

// HandleLogin authenticates the user
func HandleLogin(c *gin.Context) {
	email := c.PostForm("email")
	password := c.PostForm("password")

	var userID, passwordHash string
	err := db.DB.QueryRowContext(c.Request.Context(), "SELECT id, password_hash FROM users WHERE email = $1", email).
		Scan(&userID, &passwordHash)
	if err != nil {
		c.HTML(http.StatusUnauthorized, "login.tmpl", gin.H{
			"Error": "Invalid email or password",
		})
		return
	}

	// For standard dev fixtures where hash is literally 'REPLACE_WITH_REAL_HASH',
	// let's allow a fallback or compare against 'password' (or hashed password).
	// In a real database, it will be a proper bcrypt hash.
	if passwordHash == "REPLACE_WITH_REAL_HASH" {
		// Set a temporary dev behavior: password "password" is accepted for unhashed dev entries
		if password != "password" {
			c.HTML(http.StatusUnauthorized, "login.tmpl", gin.H{
				"Error": "Invalid email or password",
			})
			return
		}
	} else {
		err = bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password))
		if err != nil {
			c.HTML(http.StatusUnauthorized, "login.tmpl", gin.H{
				"Error": "Invalid email or password",
			})
			return
		}
	}

	auth.SetSessionCookie(c.Writer, userID)
	c.Redirect(http.StatusSeeOther, "/")
}

// HandleLogout terminates the session
func HandleLogout(c *gin.Context) {
	auth.ClearSessionCookie(c.Writer)
	c.Redirect(http.StatusSeeOther, "/login")
}
