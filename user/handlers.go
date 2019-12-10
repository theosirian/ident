package user

import (
	"encoding/json"
	"fmt"
	"strings"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	dbconf "github.com/kthomas/go-db-config"
	uuid "github.com/kthomas/go.uuid"
	"github.com/provideapp/ident/common"
	"github.com/provideapp/ident/token"
	provide "github.com/provideservices/provide-go"
)

// InstallPublicUserAPI installs unauthenticated API handlers using the given gin Engine
func InstallPublicUserAPI(r *gin.Engine) {
	r.POST("/api/v1/authenticate", authenticationHandler)
	r.POST("/api/v1/users/reset_password", userResetPasswordRequestHandler)
	r.POST("/api/v1/users/reset_password/:token", userResetPasswordHandler)
}

// InstallUserAPI installs handlers using the given gin Engine which require API authorization
func InstallUserAPI(r *gin.Engine) {
	r.GET("/api/v1/users", usersListHandler)
	r.GET("/api/v1/users/:id", userDetailsHandler)
	r.POST("/api/v1/users", createUserHandler)
	r.PUT("/api/v1/users/:id", updateUserHandler)
	r.DELETE("/api/v1/users/:id", deleteUserHandler)
}

func authenticationHandler(c *gin.Context) {
	bearer := token.InContext(c)

	buf, err := c.GetRawData()
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	params := map[string]interface{}{}
	err = json.Unmarshal(buf, &params)
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	if bearer == nil || bearer.UserID == nil {
		if email, ok := params["email"].(string); ok {
			if pw, pwok := params["password"].(string); pwok {
				var appID *uuid.UUID
				if bearer != nil && bearer.ApplicationID != nil && *bearer.ApplicationID != uuid.Nil {
					appID = bearer.ApplicationID
				} else if applicationID, applicationIDOk := params["application_id"].(string); applicationIDOk {
					appUUID, err := uuid.FromString(applicationID)
					if err != nil {
						msg := fmt.Sprintf("malformed application_id provided; %s", err.Error())
						provide.RenderError(msg, 422, c)
						return
					}
					appID = &appUUID
				}
				var scope *string
				if reqScope, reqScopeOk := params["scope"].(string); reqScopeOk {
					scope = &reqScope
				}
				resp, err := AuthenticateUser(email, pw, appID, scope)
				if err != nil {
					provide.RenderError(err.Error(), 401, c)
					return
				}
				provide.Render(resp, 201, c)
				return
			} else if bearer.ApplicationID != nil {
				resp, err := AuthenticateApplicationUser(email, *bearer.ApplicationID)
				if err != nil {
					provide.RenderError(err.Error(), 401, c)
					return
				}
				provide.Render(resp, 201, c)
				return
			}
			msg := fmt.Sprintf("password required to attempt user authentication; email address: %s", email)
			provide.RenderError(msg, 422, c)
			return
		}
	}

	provide.RenderError("unauthorized", 401, c)
}

func usersListHandler(c *gin.Context) {
	bearer := token.InContext(c)
	if bearer == nil || (bearer.ApplicationID == nil && !bearer.HasAnyPermission(common.ListUsers, common.Sudo)) {
		provide.RenderError("forbidden", 403, c)
		return
	}

	users := []*User{}
	query := dbconf.DatabaseConnection()

	if c.Query("email") != "" {
		query = query.Where("email = ?", strings.ToLower(c.Query("email")))
	}

	query.Find(&users)

	if c.Query("enrich") == "true" {
		for _, usr := range users {
			usr.enrich()
		}
	}

	provide.Render(users, 200, c)
}

func userDetailsHandler(c *gin.Context) {
	bearer := token.InContext(c)
	if bearer == nil || (!bearer.HasAnyPermission(common.ListUsers, common.Sudo) && bearer.UserID != nil && bearer.UserID.String() != c.Param("id")) {
		provide.RenderError("forbidden", 403, c)
		return
	}

	user := &User{}
	query := dbconf.DatabaseConnection().Where("id = ?", c.Param("id"))
	if bearer.ApplicationID != nil {
		query = query.Where("application_id = ?", bearer.ApplicationID)
	}

	query.Find(&user)
	if user.ID == uuid.Nil {
		provide.RenderError("user not found", 404, c)
		return
	}
	user.enrich()

	provide.Render(user.AsResponse(), 200, c)
}

func createUserHandler(c *gin.Context) {
	bearer := token.InContext(c)
	if bearer != nil || (bearer.ApplicationID == nil && !bearer.HasAnyPermission(common.CreateUser, common.Sudo)) {
		provide.RenderError("forbidden", 403, c)
		return
	}

	buf, err := c.GetRawData()
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	params := map[string]interface{}{}
	err = json.Unmarshal(buf, &params)
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	user := &User{}
	err = json.Unmarshal(buf, user)
	if err != nil {
		provide.RenderError(err.Error(), 422, c)
		return
	}

	if user.Email == nil {
		provide.RenderError("email address required", 422, c)
		return
	}

	if bearer != nil {
		user.ApplicationID = bearer.ApplicationID
	} else if appID, appIDOk := params["application_id"].(string); appIDOk {
		appUUID, err := uuid.FromString(appID)
		if err != nil {
			msg := fmt.Sprintf("malformed application_id provided; %s", err.Error())
			provide.RenderError(msg, 422, c)
			return
		}
		user.ApplicationID = &appUUID
	}

	if password, passwordOk := params["password"].(string); passwordOk {
		user.Password = common.StringOrNil(password)
	}

	if _, permissionsOk := params["permissions"]; permissionsOk && !bearer.HasAnyPermission(common.UpdateUser, common.Sudo) {
		provide.RenderError("insufficient permissions to modifiy user permissions", 403, c)
		return
	}

	if Exists(*user.Email, user.ApplicationID) {
		msg := fmt.Sprintf("user exists: %s", *user.Email)
		provide.RenderError(msg, 409, c)
		return
	}

	createAuth0User := !common.IsAuth0(c)
	vendLegacyToken := true
	success, resp := user.Create(createAuth0User, vendLegacyToken)

	if success {
		provide.Render(resp, 201, c)
	} else {
		obj := map[string]interface{}{}
		obj["errors"] = user.Errors
		provide.Render(obj, 422, c)
	}
}

func updateUserHandler(c *gin.Context) {
	bearer := token.InContext(c)
	if bearer == nil || (bearer.UserID != nil && bearer.UserID.String() != c.Param("id") || !bearer.HasAnyPermission(common.UpdateUser, common.Sudo)) {
		provide.RenderError("forbidden", 403, c)
		return
	}

	buf, err := c.GetRawData()
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	params := map[string]interface{}{}
	err = json.Unmarshal(buf, &params)
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	if _, permissionsOk := params["permissions"]; permissionsOk && !bearer.HasAnyPermission(common.UpdateUser, common.Sudo) {
		provide.RenderError("insufficient permissions to modifiy user permissions", 403, c)
		return
	}

	rehashPassword := false
	if _, pwok := params["password"].(string); pwok {
		rehashPassword = true
	}

	user := &User{}
	dbconf.DatabaseConnection().Where("id = ?", c.Param("id")).Find(&user)
	if user.ID == uuid.Nil {
		provide.RenderError("user not found", 404, c)
		return
	}

	err = json.Unmarshal(buf, user)
	if err != nil {
		provide.RenderError(err.Error(), 422, c)
		return
	}

	if bearer != nil && !bearer.HasAnyPermission(common.UpdateUser, common.Sudo) {
		user.ApplicationID = bearer.ApplicationID
	}

	if rehashPassword {
		user.Password = common.StringOrNil(params["password"].(string))
		user.rehashPassword()
	}

	if user.Update() {
		provide.Render(nil, 204, c)
	} else {
		obj := map[string]interface{}{}
		obj["errors"] = user.Errors
		provide.Render(obj, 422, c)
	}
}

func deleteUserHandler(c *gin.Context) {
	bearer := token.InContext(c)
	if !bearer.HasAnyPermission(common.DeleteUser, common.Sudo) {
		provide.RenderError("forbidden", 403, c)
		return
	}

	user := &User{}
	query := dbconf.DatabaseConnection().Where("id = ?", c.Param("id"))

	if bearer.ApplicationID != nil {
		query = query.Where("application_id = ?", bearer.ApplicationID.String())
	}

	query.Find(&user)
	if user.ID == uuid.Nil {
		provide.RenderError("user not found", 404, c)
		return
	}

	if user.Delete() {
		provide.Render(nil, 204, c)
	} else {
		provide.RenderError("user deletion failed", 500, c)
	}
}

func userResetPasswordRequestHandler(c *gin.Context) {
	bearer := token.InContext(c)

	buf, err := c.GetRawData()
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	params := map[string]interface{}{}
	err = json.Unmarshal(buf, &params)
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	email, emailOk := params["email"].(string)
	if !emailOk {
		provide.RenderError("email address is required", 422, c)
		return
	}

	db := dbconf.DatabaseConnection()
	user := &User{}
	query := db.Where("email = ?", email)
	if bearer != nil && bearer.ApplicationID != nil {
		query = query.Where("application_id = ?", bearer.ApplicationID.String())
	}
	query.Find(&user)

	if user == nil || user.ID == uuid.Nil {
		provide.RenderError("user not found", 404, c)
		return
	}

	if user.CreateResetPasswordToken(db) {
		provide.Render(user.ResetPasswordTokenResponse(), 201, c)
	} else {
		obj := map[string]interface{}{}
		obj["errors"] = user.Errors
		provide.Render(obj, 422, c)
	}
}

func userResetPasswordHandler(c *gin.Context) {
	bearer := token.InContext(c)

	buf, err := c.GetRawData()
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	params := map[string]interface{}{}
	err = json.Unmarshal(buf, &params)
	if err != nil {
		provide.RenderError(err.Error(), 400, c)
		return
	}

	rawToken := c.Param("token")
	jwtToken, err := jwt.Parse(rawToken, func(_jwtToken *jwt.Token) (interface{}, error) {
		// if keyfunc != nil {
		// 	fn := *keyfunc
		// 	return fn(_jwtToken)
		// }
		return nil, nil
	})

	if jwtToken == nil {
		// provide.RenderError(fmt.Sprintf("invalid jwt token; %s", err.Error()), 422, c)
		provide.RenderError("invalid jwt token", 422, c)
		return
	}

	var userID *uuid.UUID
	if claims, ok := jwtToken.Claims.(jwt.MapClaims); ok {
		if sub, subok := claims["sub"].(string); subok {
			subprts := strings.Split(sub, ":")
			if len(subprts) != 2 {
				provide.RenderError(fmt.Sprintf("JWT subject malformed; %s", sub), 422, c)
				return
			}
			if subprts[0] != "user" {
				provide.RenderError(fmt.Sprintf("JWT claims specified non-user subject: %s", subprts[0]), 422, c)
				return
			}
			id, err := uuid.FromString(subprts[1])
			if err != nil {
				provide.RenderError(fmt.Sprintf("invalid user id; %s", err.Error()), 422, c)
				return
			}
			userID = &id
		}
	}

	if userID == nil || *userID == uuid.Nil {
		provide.RenderError("invalid user id", 422, c)
		return
	}

	password, passwordOk := params["password"].(string)
	if !passwordOk {
		provide.RenderError("password is required", 422, c)
		return
	}

	db := dbconf.DatabaseConnection()
	user := &User{}
	query := db.Where("id = ?", userID.String())
	if bearer != nil && bearer.ApplicationID != nil {
		query = query.Where("application_id = ?", bearer.ApplicationID.String())
	}
	query.Find(&user)

	if user == nil || user.ID == uuid.Nil {
		provide.RenderError("user not found", 404, c)
		return
	}

	if user.ResetPasswordToken == nil || *user.ResetPasswordToken != rawToken {
		provide.RenderError("invalid reset token", 422, c)
		return
	}

	user.Password = common.StringOrNil(password)
	user.rehashPassword()

	if user.Update() {
		provide.Render(nil, 204, c)
	} else {
		obj := map[string]interface{}{}
		obj["errors"] = user.Errors
		provide.Render(obj, 422, c)
	}
}
