package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"restaurant-saas/internal/db"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type TeamMember struct {
	ID       string
	Username string
	FullName string
	Email    string
	Role     string
	Status   string
}

func ShowTeam(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	user := value.(CurrentUser)
	restaurantID := GetActiveRestaurantID(c, user)
	var members []TeamMember
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		if _, err := restaurantAccount(c.Request.Context(), tx, restaurantID, user.ID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(c.Request.Context(), `
			SELECT u.id, u.username, u.full_name, u.email, ru.role, ru.status
			FROM restaurant_users ru JOIN users u ON u.id = ru.user_id
			WHERE ru.restaurant_id = $1 ORDER BY u.full_name, u.username
		`, restaurantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var member TeamMember
			if err := rows.Scan(&member.ID, &member.Username, &member.FullName, &member.Email, &member.Role, &member.Status); err != nil {
				return err
			}
			members = append(members, member)
		}
		return rows.Err()
	})
	if err != nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	c.HTML(http.StatusOK, "team.tmpl", gin.H{
		"User": user, "ActiveRestaurantID": restaurantID, "ActiveNav": "team", "Members": members,
		"Error": c.Query("error"), "Notice": c.Query("notice"),
	})
}

func accountUserLimit(ctx context.Context, tx *sql.Tx, accountID, restaurantID string) (int, error) {
	var lockedAccount string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE id = $1 FOR UPDATE", accountID).Scan(&lockedAccount); err != nil {
		return 0, err
	}
	limit, err := GetLimit(ctx, accountID, "max_users_per_account")
	if err != nil {
		return 0, fmt.Errorf("user limit is unavailable for this account")
	}
	return limit, nil
}

func ensureAccountUserCapacity(ctx context.Context, tx *sql.Tx, accountID, restaurantID, userID string, limit int) error {
	var alreadyMember bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM restaurant_users ru JOIN restaurants r ON r.id = ru.restaurant_id
			WHERE r.account_id = $1 AND ru.user_id = $2 AND ru.status = 'active'
		)
	`, accountID, userID).Scan(&alreadyMember); err != nil {
		return err
	}
	if alreadyMember {
		return nil
	}
	var belongsElsewhere bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM restaurant_users ru JOIN restaurants r ON r.id = ru.restaurant_id
			WHERE r.account_id <> $1 AND ru.user_id = $2 AND ru.status = 'active'
		)
	`, accountID, userID).Scan(&belongsElsewhere); err != nil {
		return err
	}
	if belongsElsewhere {
		return fmt.Errorf("this login is already assigned to another account")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(DISTINCT ru.user_id) FROM restaurant_users ru
		JOIN restaurants r ON r.id = ru.restaurant_id
		WHERE r.account_id = $1 AND ru.status = 'active'
	`, accountID).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return fmt.Errorf("account user limit of %d reached", limit)
	}
	return nil
}

func CreateRestaurantLogin(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := value.(CurrentUser)
	restaurantID := GetActiveRestaurantID(c, user)
	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.TrimSpace(c.PostForm("email"))
	fullName := strings.TrimSpace(c.PostForm("full_name"))
	password := c.PostForm("password")
	role := c.PostForm("role")
	if username == "" || email == "" || fullName == "" || len(password) < 12 || (role != "owner" && role != "admin") {
		c.Redirect(http.StatusSeeOther, "/team?error="+url.QueryEscape("Complete all fields and use a password of at least 12 characters"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not secure the password")
		return
	}
	err = db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		accountID, err := restaurantAccount(c.Request.Context(), tx, restaurantID, user.ID)
		if err != nil {
			return err
		}
		limit, err := accountUserLimit(c.Request.Context(), tx, accountID, restaurantID)
		if err != nil {
			return err
		}
		var newUserID string
		if err := tx.QueryRowContext(c.Request.Context(), `
			INSERT INTO users (username, email, password_hash, full_name)
			VALUES ($1, $2, $3, $4) RETURNING id
		`, username, email, string(hash), fullName).Scan(&newUserID); err != nil {
			return fmt.Errorf("could not create login; username or email may already be in use")
		}
		if err := ensureAccountUserCapacity(c, tx, accountID, restaurantID, newUserID, limit); err != nil {
			return err
		}
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO restaurant_users (restaurant_id, user_id, role, status) VALUES ($1, $2, $3, 'active')
		`, restaurantID, newUserID, role)
		return err
	})
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/team?error="+url.QueryEscape(err.Error()))
		return
	}
	c.Redirect(http.StatusSeeOther, "/team?notice=Login+created+and+assigned")
}

func AddExistingRestaurantLogin(c *gin.Context) {
	value, exists := c.Get("user")
	if !exists {
		c.String(http.StatusUnauthorized, "Unauthorized")
		return
	}
	user := value.(CurrentUser)
	restaurantID := GetActiveRestaurantID(c, user)
	identifier := strings.TrimSpace(c.PostForm("identifier"))
	role := c.PostForm("role")
	if identifier == "" || (role != "owner" && role != "admin") {
		c.Redirect(http.StatusSeeOther, "/team?error=Enter+a+username+or+email+and+select+a+role")
		return
	}
	err := db.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		accountID, err := restaurantAccount(c.Request.Context(), tx, restaurantID, user.ID)
		if err != nil {
			return err
		}
		limit, err := accountUserLimit(c.Request.Context(), tx, accountID, restaurantID)
		if err != nil {
			return err
		}
		var targetUserID string
		if err := tx.QueryRowContext(c.Request.Context(), "SELECT id FROM users WHERE username = $1 OR email = $1", identifier).Scan(&targetUserID); err != nil {
			return fmt.Errorf("login was not found")
		}
		if err := ensureAccountUserCapacity(c, tx, accountID, restaurantID, targetUserID, limit); err != nil {
			return err
		}
		_, err = tx.ExecContext(c.Request.Context(), `
			INSERT INTO restaurant_users (restaurant_id, user_id, role, status) VALUES ($1, $2, $3, 'active')
			ON CONFLICT (restaurant_id, user_id) DO UPDATE SET role = EXCLUDED.role, status = 'active'
		`, restaurantID, targetUserID, role)
		return err
	})
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/team?error="+url.QueryEscape(err.Error()))
		return
	}
	c.Redirect(http.StatusSeeOther, "/team?notice=Login+assigned+to+restaurant")
}
