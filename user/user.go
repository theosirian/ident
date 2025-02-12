package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/badoux/checkmail"
	jwt "github.com/dgrijalva/jwt-go"
	"github.com/jinzhu/gorm"
	dbconf "github.com/kthomas/go-db-config"
	natsutil "github.com/kthomas/go-natsutil"
	uuid "github.com/kthomas/go.uuid"
	trumail "github.com/kthomas/trumail/verifier"
	"github.com/provideplatform/ident/common"
	"github.com/provideplatform/ident/token"
	provide "github.com/provideplatform/provide-go/api"
	util "github.com/provideplatform/provide-go/common/util"
	"golang.org/x/crypto/bcrypt"
)

const defaultResetPasswordTokenTimeout = time.Hour * 1
const identUserIDKey = "ident_user_id"
const natsSiaUserNotificationSubject = "sia.user.notification"
const natsSiaUserDeleteNotificationSubject = "sia.user.deleted"

// User model
type User struct {
	provide.Model
	ApplicationID          *uuid.UUID             `sql:"type:uuid" json:"application_id,omitempty"`
	Name                   *string                `sql:"-" json:"name"`
	FirstName              *string                `sql:"not null" json:"first_name"`
	LastName               *string                `sql:"not null" json:"last_name"`
	Email                  *string                `sql:"not null" json:"email,omitempty"`
	ExpiresAt              *time.Time             `json:"-"`
	Permissions            common.Permission      `sql:"not null" json:"permissions,omitempty"`
	EphemeralMetadata      *EphemeralUserMetadata `sql:"-" json:"metadata,omitempty"`
	Password               *string                `json:"-"`
	PrivacyPolicyAgreedAt  *time.Time             `json:"privacy_policy_agreed_at"`
	TermsOfServiceAgreedAt *time.Time             `json:"terms_of_service_agreed_at"`
	ResetPasswordToken     *string                `json:"-"`
}

// AuthenticationResponse is returned upon successful authentication using an email address
type AuthenticationResponse struct {
	User  *Response       `json:"user"`
	Token *token.Response `json:"token"`
}

// Response is preferred over writing an entire User instance as JSON
type Response struct {
	ID                     uuid.UUID              `json:"id"`
	CreatedAt              time.Time              `json:"created_at"`
	Name                   string                 `json:"name"`
	FirstName              string                 `json:"first_name"`
	LastName               string                 `json:"last_name"`
	Email                  string                 `json:"email"`
	Permissions            common.Permission      `json:"permissions,omitempty"`
	PrivacyPolicyAgreedAt  *time.Time             `json:"privacy_policy_agreed_at"`
	TermsOfServiceAgreedAt *time.Time             `json:"terms_of_service_agreed_at"`
	Metadata               *EphemeralUserMetadata `json:"metadata,omitempty"`
}

// Find returns a user for the given id
func Find(userID uuid.UUID) *User {
	db := dbconf.DatabaseConnection()
	user := &User{}
	db.Where("id = ?", userID).Find(&user)
	if user == nil || user.ID == uuid.Nil {
		return nil
	}
	return user
}

// FindByEmail returns a user for the given email address, application and organization id
func FindByEmail(email string, applicationID *uuid.UUID, organizationID *uuid.UUID) *User {
	db := dbconf.DatabaseConnection()

	user := &User{}
	query := db.Where("users.email = ?", email)

	if applicationID != nil && *applicationID != uuid.Nil {
		query = query.Joins("LEFT OUTER JOIN applications_users as au ON au.user_id = users.id AND au.application_id = ?", applicationID)
		query = query.Where("users.application_id = ?", applicationID)
	} else {
		query = query.Where("users.application_id IS NULL")
	}

	if organizationID != nil && *organizationID != uuid.Nil {
		if applicationID != nil && *applicationID != uuid.Nil {
			query = query.Joins("LEFT OUTER JOIN applications_organizations as ao ON ao.organization_id = ou.organization_id")
			query = query.Where("ao.application_id = ? AND ao.organization_id = ?", applicationID, organizationID)
		}

		query = query.Joins("LEFT OUTER JOIN organizations_users as ou ON ou.user_id = users.id")
		query = query.Where("ou.organization_id = ?", organizationID)
	}
	query.Find(&user)
	if user == nil || user.ID == uuid.Nil {
		return nil
	}
	return user
}

// Exists returns true if a user exists for the given email address, app id and org id
func Exists(email string, applicationID *uuid.UUID, organizationID *uuid.UUID) bool {
	return FindByEmail(email, applicationID, organizationID) != nil
}

// AuthenticateUser attempts to authenticate by email address and password;
// i.e., this is equivalent to grant_type=password under the OAuth 2 spec
func AuthenticateUser(tx *gorm.DB, email, password string, applicationID *uuid.UUID, scope *string) (*AuthenticationResponse, error) {
	var db *gorm.DB
	if tx != nil {
		db = tx
	} else {
		db = dbconf.DatabaseConnection()
	}

	var user = &User{}
	query := db.Where("email = ?", strings.ToLower(email))
	if applicationID != nil && *applicationID != uuid.Nil {
		query = query.Where("application_id = ?", applicationID)
	} else {
		query = query.Where("application_id IS NULL")
	}

	query.First(&user)
	if user != nil && user.ID != uuid.Nil {
		if !user.hasPermission(common.Authenticate) {
			return nil, errors.New("authentication failed due to revoked authenticate permission")
		}

		if !user.authenticate(password) {
			return nil, errors.New("authentication failed with given credentials")
		}
	} else {
		return nil, fmt.Errorf("invalid email")
	}

	token := &token.Token{
		UserID:      &user.ID,
		Scope:       scope,
		Permissions: user.Permissions,
	}

	if !token.Vend() {
		var err error
		if len(token.Errors) > 0 {
			err = fmt.Errorf("failed to create token for authenticated user: %s; %s", *user.Email, *token.Errors[0].Message)
			common.Log.Warningf(err.Error())
		}

		return &AuthenticationResponse{
			User:  user.AsResponse(),
			Token: nil,
		}, err
	}

	return &AuthenticationResponse{
		User:  user.AsResponse(),
		Token: token.AsResponse(),
	}, nil
}

// AuthenticateApplicationUser vends a user token on behalf of the owning application
func AuthenticateApplicationUser(email string, applicationID uuid.UUID, scope *string) (*AuthenticationResponse, error) {
	var user = &User{}
	db := dbconf.DatabaseConnection()
	query := db.Where("application_id = ? AND email = ?", applicationID, strings.ToLower(email))
	query.First(&user)
	if user != nil && user.ID != uuid.Nil {
		if !user.hasPermission(common.Authenticate) {
			return nil, errors.New("authentication failed due to revoked authenticate permission")
		}

		if user.Password != nil {
			return nil, errors.New("application user authentication not currently supported if user password is set")
		}
	} else {
		return nil, errors.New("application user authentication failed with given credentials")
	}
	if user.ExpiresAt != nil && time.Now().After(*user.ExpiresAt) {
		return nil, errors.New("user authentication failed; user account has expired")
	}

	token := &token.Token{
		UserID:      &user.ID,
		Scope:       scope,
		Permissions: user.Permissions,
	}
	if !token.Vend() {
		var err error
		if len(token.Errors) > 0 {
			err = fmt.Errorf("failed to create token for application-authenticated user: %s; %s", *user.Email, *token.Errors[0].Message)
			common.Log.Warningf(err.Error())
		}
		return &AuthenticationResponse{
			User:  user.AsResponse(),
			Token: nil,
		}, err
	}
	return &AuthenticationResponse{
		User:  user.AsResponse(),
		Token: token.AsResponse(),
	}, nil
}

// authenticate returns true if the User can be authenticated using the given password
func (u *User) authenticate(password string) bool {
	if u.Password == nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(*u.Password), []byte(password)) == nil
}

// hasPermission returns true if the permissioned User has the given permissions
func (u *User) hasPermission(permission common.Permission) bool {
	return u.Permissions.Has(permission)
}

// hasAnyPermission returns true if the permissioned User has any the given permissions
func (u *User) hasAnyPermission(permissions ...common.Permission) bool {
	for _, p := range permissions {
		if u.hasPermission(p) {
			return true
		}
	}
	return false
}

// Create and persist a user
func (u *User) Create(tx *gorm.DB, createAuth0User bool) bool {
	var db *gorm.DB
	if tx != nil {
		db = tx
	} else {
		db = dbconf.DatabaseConnection()
		db = db.Begin()
		defer db.RollbackUnlessCommitted()
	}

	if !u.validate() {
		return false
	}

	if db.NewRecord(u) {
		result := db.Create(&u)
		rowsAffected := result.RowsAffected
		errors := result.GetErrors()
		if len(errors) > 0 {
			for _, err := range errors {
				u.Errors = append(u.Errors, &provide.Error{
					Message: common.StringOrNil(err.Error()),
				})
			}
		}
		if !db.NewRecord(u) {
			success := rowsAffected > 0
			if success {
				common.Log.Debugf("created user: %s", *u.Email)

				if createAuth0User && common.Auth0IntegrationEnabled && !common.Auth0IntegrationCustomDatabase {
					err := u.createAuth0User()
					if err != nil {
						u.Errors = append(u.Errors, &provide.Error{
							Message: common.StringOrNil(err.Error()),
						})
						return false
					}
				}

				if tx == nil {
					db.Commit()
				}

				if success && (u.ApplicationID == nil || *u.ApplicationID == uuid.Nil) && common.DispatchSiaNotifications {
					payload, _ := json.Marshal(map[string]interface{}{
						"id": u.ID.String(),
					})
					natsutil.NatsJetstreamPublish(natsSiaUserNotificationSubject, payload)
				}

				return success
			}
		}
	}

	return false
}

// Update an existing user
func (u *User) Update() bool {
	db := dbconf.DatabaseConnection()

	if !u.validate() {
		return false
	}

	tx := db.Begin()
	result := tx.Save(&u)
	success := result.RowsAffected > 0
	errors := result.GetErrors()
	if len(errors) > 0 {
		for _, err := range errors {
			u.Errors = append(u.Errors, &provide.Error{
				Message: common.StringOrNil(err.Error()),
			})
		}
	}

	if success && common.Auth0IntegrationEnabled && !common.Auth0IntegrationCustomDatabase {
		err := u.updateAuth0User()
		if err != nil {
			u.Errors = append(u.Errors, &provide.Error{
				Message: common.StringOrNil(err.Error()),
			})
			tx.Rollback()
			return false
		}
	}

	common.Log.Debugf("updated user: %s", *u.Email)
	tx.Commit()
	return success
}

func (u *User) addApplicationAssociation(tx *gorm.DB, appID uuid.UUID, permissions common.Permission) bool {
	var db *gorm.DB
	if tx != nil {
		db = tx
	} else {
		db = dbconf.DatabaseConnection()
	}

	common.Log.Debugf("adding user %s to application: %s", u.ID, appID)
	result := db.Exec("INSERT INTO applications_users (application_id, user_id, permissions) VALUES (?, ?, ?)", appID, u.ID, permissions)
	success := result.RowsAffected == 1
	if success {
		common.Log.Debugf("added user %s to application: %s", u.ID, appID)
	} else {
		common.Log.Warningf("failed to add user %s to application: %s", u.ID, appID)
		errors := result.GetErrors()
		if len(errors) > 0 {
			for _, err := range errors {
				u.Errors = append(u.Errors, &provide.Error{
					Message: common.StringOrNil(err.Error()),
				})
			}
		}
	}
	return success
}

func (u *User) addOrganizationAssociation(tx *gorm.DB, orgID uuid.UUID, permissions common.Permission) bool {
	var db *gorm.DB
	if tx != nil {
		db = tx
	} else {
		db = dbconf.DatabaseConnection()
	}

	common.Log.Debugf("adding user %s to organization: %s", u.ID, orgID)
	result := db.Exec("INSERT INTO organizations_users (organization_id, user_id, permissions) VALUES (?, ?, ?)", orgID, u.ID, permissions)
	success := result.RowsAffected == 1
	if success {
		common.Log.Debugf("added user %s to organization: %s", u.ID, orgID)
	} else {
		common.Log.Warningf("failed to add user %s to organization: %s", u.ID, orgID)
		errors := result.GetErrors()
		if len(errors) > 0 {
			for _, err := range errors {
				u.Errors = append(u.Errors, &provide.Error{
					Message: common.StringOrNil(err.Error()),
				})
			}
		}
	}
	return success
}

func (u *User) verifyEmailAddress() bool {
	var validEmailAddress bool
	if u.Email != nil {
		u.Email = common.StringOrNil(strings.ToLower(*u.Email))
		err := checkmail.ValidateFormat(*u.Email)
		validEmailAddress = err == nil
		if err != nil {
			u.Errors = append(u.Errors, &provide.Error{
				Message: common.StringOrNil(fmt.Sprintf("invalid email address: %s; %s", *u.Email, err.Error())),
			})
		}

		if common.PerformEmailVerification {
			emailVerificationErr := u.VerifyEmailDeliverability()
			if emailVerificationErr != nil {
				u.Errors = append(u.Errors, &provide.Error{
					Message: common.StringOrNil(emailVerificationErr.Error()),
				})
			}
		}
	}
	return validEmailAddress
}

// VerifyEmailDeliverability attempts to verify the user's email address is still deliverable;
// this must be called from a host that has not been blacklisted, or it could have unintended consequences...
func (u *User) VerifyEmailDeliverability() error {
	common.Log.Debugf("attempting to verify deliverability for email address: %s", *u.Email)

	var err error
	emailVerifier := trumail.NewVerifier(
		common.EmailVerificationFromDomain,
		common.EmailVerificationFromAddress,
		common.EmailVerificationTimeout,
		common.EmailVerificationAttempts,
	)

	lookup, err := emailVerifier.Verify(*u.Email)
	if err != nil {
		err = fmt.Errorf("email address verification failed: %s; %s", *u.Email, err.Error())
	} else if !lookup.Deliverable && !lookup.CatchAll {
		err = fmt.Errorf("email address verification failed: %s; undeliverable", *u.Email)
	} else if lookup.CatchAll {
		err = fmt.Errorf("email address verification failed: %s; mail server exists but inbox is invalid", *u.Email)
	} else if !lookup.Deliverable {
		err = fmt.Errorf("email address verification failed: %s; undeliverable", *u.Email)
	}

	return err
}

// FullName returns the user's full name
func (u *User) FullName() *string {
	name := ""
	if u.FirstName != nil {
		name = *u.FirstName
	}
	if u.LastName != nil {
		name = fmt.Sprintf("%s %s", name, *u.LastName)
	}
	return common.StringOrNil(name)
}

// Enrich attempts to enrich the user; currently this only supports any user/app metadata on the
// user's associated auth0 record, enriching `u.EphemeralMetadata`; no-op if auth0 is not configured
func (u *User) Enrich() error {
	u.Name = u.FullName()

	if !common.Auth0IntegrationEnabled {
		return nil
	}

	auth0User, _ := u.fetchAuth0User()
	// if err != nil {
	// 	return err
	// }

	if auth0User != nil && auth0User.AppMetadata != nil && auth0User.AppMetadata[identUserIDKey] == nil {
		auth0User.AppMetadata[identUserIDKey] = u.ID
	}

	if auth0User != nil {
		u.EphemeralMetadata = auth0User
	}

	return nil
}

// Reload the user
func (u *User) Reload() {
	db := dbconf.DatabaseConnection()
	db.Model(&u).Find(u)
}

// validate a user for persistence
func (u *User) validate() bool {
	u.Errors = make([]*provide.Error, 0)
	db := dbconf.DatabaseConnection()
	if db.NewRecord(u) {
		if u.Password != nil || u.ApplicationID == nil {
			u.verifyEmailAddress()
			u.rehashPassword()
		}
		if u.Permissions == 0 {
			u.Permissions = common.DefaultUserPermission
		}
	}
	return len(u.Errors) == 0
}

func (u *User) rehashPassword() {
	if u.Password != nil {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(*u.Password), bcrypt.DefaultCost)
		if err != nil {
			u.Password = nil
			u.Errors = append(u.Errors, &provide.Error{
				Message: common.StringOrNil(err.Error()),
			})
		} else {
			u.Password = common.StringOrNil(string(hashedPassword))
			u.ResetPasswordToken = nil
		}
	} else {
		u.Errors = append(u.Errors, &provide.Error{
			Message: common.StringOrNil("invalid password"),
		})
	}
}

// Delete a user
func (u *User) Delete() bool {
	db := dbconf.DatabaseConnection()
	tx := db.Begin()
	result := tx.Delete(&u)
	errors := result.GetErrors()
	if len(errors) > 0 {
		for _, err := range errors {
			u.Errors = append(u.Errors, &provide.Error{
				Message: common.StringOrNil(err.Error()),
			})
		}
	}
	success := len(u.Errors) == 0
	if success && common.Auth0IntegrationEnabled {
		common.Log.Debugf("deleted user: %s", *u.Email)

		if common.Auth0IntegrationEnabled {
			err := u.deleteAuth0User()
			if err != nil {
				u.Errors = append(u.Errors, &provide.Error{
					Message: common.StringOrNil(err.Error()),
				})
				tx.Rollback()
				return false
			}
		}

		if common.DispatchSiaNotifications {
			payload, _ := json.Marshal(map[string]interface{}{
				"user_id": u.ID.String(),
			})
			natsutil.NatsJetstreamPublish(natsSiaUserDeleteNotificationSubject, payload)
		}
	}
	tx.Commit()
	return success
}

// AsResponse marshals a user into a user response
func (u *User) AsResponse() *Response {
	return &Response{
		ID:                     u.ID,
		CreatedAt:              u.CreatedAt,
		Name:                   *(u.FullName()),
		FirstName:              *u.FirstName,
		LastName:               *u.LastName,
		Email:                  *u.Email,
		Metadata:               u.EphemeralMetadata,
		Permissions:            u.Permissions,
		PrivacyPolicyAgreedAt:  u.PrivacyPolicyAgreedAt,
		TermsOfServiceAgreedAt: u.TermsOfServiceAgreedAt,
	}
}

// requestPasswordReset attempts to dispatch a reset password token
func (u *User) requestPasswordReset(db *gorm.DB) bool {
	if u.CreateResetPasswordToken(db) {
		common.Log.Debugf("created reset password token for user: %s", u.ID)
		common.Log.Warningf("TODO: dispatch reset password token to user out-of-band...")
		common.Log.Debugf("%s", *u.ResetPasswordToken)
		return true
	}

	return false
}

// CreateResetPasswordToken creates a reset password token
func (u *User) CreateResetPasswordToken(db *gorm.DB) bool {
	issuedAt := time.Now()
	tokenID, err := uuid.NewV4()
	if err != nil {
		common.Log.Warningf("failed to generate reset password JWT token; %s", err.Error())
		return false
	}
	appClaims := map[string]interface{}{
		"name":       u.FullName(),
		"first_name": u.FirstName,
		"last_name":  u.LastName,
	}
	if u.ApplicationID != nil {
		appClaims["application_id"] = u.ApplicationID
	}
	claims := map[string]interface{}{
		"jti":                        tokenID,
		"exp":                        issuedAt.Add(defaultResetPasswordTokenTimeout).Unix(),
		"iat":                        issuedAt.Unix(),
		"sub":                        fmt.Sprintf("user:%s", u.ID.String()),
		util.JWTApplicationClaimsKey: appClaims,
	}

	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims))
	token, err := jwtToken.SignedString([]byte{})
	if err != nil {
		common.Log.Warningf("failed to sign reset password JWT token; %s", err.Error())
		return false
	}

	u.ResetPasswordToken = common.StringOrNil(token)

	result := db.Save(u)
	errors := result.GetErrors()
	if len(errors) > 0 {
		for _, err := range errors {
			u.Errors = append(u.Errors, &provide.Error{
				Message: common.StringOrNil(err.Error()),
			})
		}
		return false
	}
	return true
}
