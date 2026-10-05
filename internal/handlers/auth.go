package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"restaurant-saas/internal/auth"
	"restaurant-saas/internal/db"
	"restaurant-saas/internal/models"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// CurrentUser represents the logged-in user and their restaurant contexts
type CurrentUser struct {
	ID               string
	Email            string
	FullName         string
	IsPlatformAdmin  bool
	PlatformRole     string
	Restaurants      []models.RestaurantUserContext
	CanManageAccount bool
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
		if user.IsPlatformAdmin {
			auth.ClearSessionCookie(c.Writer)
			c.Redirect(http.StatusSeeOther, "/platform/login")
			c.Abort()
			return
		}

		// Fetch memberships in a transaction so the RLS user context is set.
		if !user.IsPlatformAdmin {
			err = db.WithTx(ctx, func(tx *sql.Tx) error {
				rows, err := tx.QueryContext(ctx, `
					SELECT ru.restaurant_id, r.name, ru.role
					FROM restaurant_users ru
					JOIN restaurants r ON r.id = ru.restaurant_id
					JOIN accounts a ON a.id = r.account_id AND a.status = 'active'
					WHERE ru.user_id = $1 AND ru.status = 'active'
				`, userID)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var rc models.RestaurantUserContext
					if err := rows.Scan(&rc.RestaurantID, &rc.RestaurantName, &rc.Role); err != nil {
						return err
					}
					user.Restaurants = append(user.Restaurants, rc)
				}
				return rows.Err()
			})
			if err != nil {
				auth.ClearSessionCookie(c.Writer)
				c.Redirect(http.StatusSeeOther, "/login")
				c.Abort()
				return
			}
		}
		if len(user.Restaurants) == 0 && !user.IsPlatformAdmin {
			auth.ClearSessionCookie(c.Writer)
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		if !user.IsPlatformAdmin {
			_ = db.DB.QueryRowContext(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM accounts a JOIN restaurants r ON r.account_id = a.id
					JOIN restaurant_users ru ON ru.restaurant_id = r.id
					WHERE a.owner_user_id = $1 AND ru.user_id = $1 AND ru.status = 'active'
				)
			`, userID).Scan(&user.CanManageAccount)
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
	username := c.PostForm("username")
	if username == "" {
		username = email
	}
	err := db.DB.QueryRowContext(c.Request.Context(), `
		SELECT u.id, u.password_hash FROM users u
		WHERE u.username = $1 AND NOT EXISTS (SELECT 1 FROM platform_admins pa WHERE pa.user_id = u.id)
	`, username).
		Scan(&userID, &passwordHash)
	if err != nil {
		c.HTML(http.StatusUnauthorized, "login.tmpl", gin.H{
			"Error": "Invalid email or password",
		})
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password))
	if err != nil {
		c.HTML(http.StatusUnauthorized, "login.tmpl", gin.H{
			"Error": "Invalid email or password",
		})
		return
	}

	auth.SetSessionCookie(c.Writer, userID)
	c.Redirect(http.StatusSeeOther, "/")
}

func ShowPlatformLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "platform_login.tmpl", gin.H{"Error": c.Query("error")})
}

func HandlePlatformLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	var userID, passwordHash string
	err := db.DB.QueryRowContext(c.Request.Context(), `
		SELECT u.id, u.password_hash FROM users u
		JOIN platform_admins pa ON pa.user_id = u.id
		WHERE u.username = $1
	`, username).Scan(&userID, &passwordHash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		c.HTML(http.StatusUnauthorized, "platform_login.tmpl", gin.H{"Error": "Invalid platform credentials"})
		return
	}
	auth.SetPlatformSessionCookie(c.Writer, userID)
	c.Redirect(http.StatusSeeOther, "/platform")
}

func RequirePlatformAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(auth.PlatformCookieName)
		if err != nil {
			c.Redirect(http.StatusSeeOther, "/platform/login")
			c.Abort()
			return
		}
		userID, err := auth.VerifySessionToken(token)
		if err != nil {
			auth.ClearPlatformSessionCookie(c.Writer)
			c.Redirect(http.StatusSeeOther, "/platform/login")
			c.Abort()
			return
		}
		ctx := context.WithValue(c.Request.Context(), db.UserIDKey, userID)
		c.Request = c.Request.WithContext(ctx)
		var user CurrentUser
		user.ID = userID
		if err := db.DB.QueryRowContext(ctx, "SELECT email, full_name FROM users WHERE id = $1", userID).Scan(&user.Email, &user.FullName); err != nil {
			auth.ClearPlatformSessionCookie(c.Writer)
			c.Redirect(http.StatusSeeOther, "/platform/login")
			c.Abort()
			return
		}
		if err := db.DB.QueryRowContext(ctx, "SELECT role FROM platform_admins WHERE user_id = $1", userID).Scan(&user.PlatformRole); err != nil {
			auth.ClearPlatformSessionCookie(c.Writer)
			c.Redirect(http.StatusSeeOther, "/platform/login")
			c.Abort()
			return
		}
		user.IsPlatformAdmin = true
		c.Set("user", user)
		c.Next()
	}
}

func HandlePlatformLogout(c *gin.Context) {
	auth.ClearPlatformSessionCookie(c.Writer)
	c.Redirect(http.StatusSeeOther, "/platform/login")
}

// HandleLogout terminates the session
func HandleLogout(c *gin.Context) {
	auth.ClearSessionCookie(c.Writer)
	c.Redirect(http.StatusSeeOther, "/login")
}
