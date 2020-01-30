package organization

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	dbconf "github.com/kthomas/go-db-config"
	uuid "github.com/kthomas/go.uuid"
	"github.com/provideapp/ident/common"
	"github.com/provideapp/ident/token"
	"github.com/provideapp/ident/user"
	provide "github.com/provideservices/provide-go"
)

// InstallOrganizationAPI installs handlers using the given gin Engine
func InstallOrganizationAPI(r *gin.Engine) {
	r.GET("/api/v1/organizations", organizationsListHandler)
	r.GET("/api/v1/organizations/:id", organizationDetailsHandler)
	r.POST("/api/v1/organizations", createOrganizationHandler)
	r.PUT("/api/v1/organizations/:id", updateOrganizationHandler)
	r.DELETE("/api/v1/organizations/:id", deleteOrganizationHandler)
}

// InstallOrganizationUsersAPI installs the handlers using the given gin Engine
func InstallOrganizationUsersAPI(r *gin.Engine) {
	r.GET("/api/v1/organizations/:id/users", organizationUsersListHandler)
	r.POST("/api/v1/organizations/:id/users", createOrganizationUserHandler)
	r.PUT("/api/v1/organizations/:id/users/:userId", updateOrganizationUserHandler)
	r.DELETE("/api/v1/organizations/:id/users/:userId", deleteOrganizationUserHandler)

	r.GET("/api/v1/organizations/:id/invitations", organizationInvitationsListHandler)
}

func resolveOrganization(db *gorm.DB, orgID, appID, userID *uuid.UUID) *gorm.DB {
	query := db.Joins("JOIN applications_organizations as ao ON ao.organization_id = organizations.id")
	if appID != nil {
		query = query.Where("ao.application_id = ?", appID)
	}
	if orgID != nil {
		query = query.Where("ao.organization_id = ?", orgID)
	}
	if userID != nil {
		query = query.Joins("JOIN organizations_users as ou ON ou.organization_id = organizations.id").Where("ou.user_id = ?", userID)
	}
	return query.Order("organizations.name DESC").Group("organizations.id")
}

func resolveOrganizationUsers(db *gorm.DB, orgID uuid.UUID, appID *uuid.UUID) *gorm.DB {
	query := db.Select("users.id, users.created_at, users.name, ou.permissions as permissions")
	query = query.Joins("JOIN organizations_users as ou ON ou.user_id = users.id").Where("ou.organization_id = ?", orgID)
	if appID != nil {
		query = query.Joins("JOIN applications_organizations as ao ON ao.organization_id = ou.organization_id").Where("ao.application_id = ?", appID)
	}
	return query
}

func organizationsListHandler(c *gin.Context) {
	bearer := token.InContext(c)
	applicationID := bearer.ApplicationID
	userID := bearer.UserID

	if (userID == nil || *userID == uuid.Nil) && (applicationID == nil || *applicationID == uuid.Nil) {
		provide.RenderError("unauthorized", 401, c)
		return
	}

	var orgs []*Organization

	query := dbconf.DatabaseConnection()
	query = resolveOrganization(query, nil, applicationID, userID)
	provide.Paginate(c, query, &Organization{}).Find(&orgs)
	provide.Render(orgs, 200, c)
}

func organizationDetailsHandler(c *gin.Context) {
	provide.RenderError("not implemented", 501, c)
}

func createOrganizationHandler(c *gin.Context) {
	bearer := token.InContext(c)
	userID := bearer.UserID

	if userID == nil || *userID == uuid.Nil {
		provide.RenderError("unauthorized", 401, c)
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

	org := &Organization{}
	err = json.Unmarshal(buf, &org)
	if err != nil {
		provide.RenderError(err.Error(), 422, c)
		return
	}
	org.UserID = userID

	var invite *user.Invite
	var permissions common.Permission

	if invitationToken, invitationTokenOk := params["invitation_token"].(string); invitationTokenOk {
		invite, err = user.ParseInvite(invitationToken, false)
		if err != nil {
			provide.RenderError(err.Error(), 422, c)
			return
		}

		if invite.OrganizationID != nil {
			provide.RenderError("invitation contained specific organization_id", 400, c)
			return
		}

		if invite.UserID != nil && userID != nil && invite.UserID.String() != userID.String() {
			provide.RenderError("invitation user_id did not match authorized user", 403, c)
			return
		}

		if invite.Permissions != nil {
			permissions = *invite.Permissions
		} else {
			permissions = common.DefaultApplicationResourcePermission
		}
	}

	if _, permissionsOk := params["permissions"]; permissionsOk {
		provide.RenderError("unable to assert arbitrary organization permissions", 403, c)
		return
	}

	db := dbconf.DatabaseConnection()
	tx := db.Begin()
	success := org.Create(tx)
	if success && invite != nil {
		if invite.ApplicationID != nil {
			success = org.addApplicationAssociation(tx, *invite.ApplicationID, permissions)
		}

		if success {
			success = invite.Token.IsRevoked() || invite.Token.Revoke(tx)
			if success {
				invite.InvalidateCache()
			}
		}
	}

	if success {
		tx.Commit()
		provide.Render(org, 201, c)
	} else {
		tx.Rollback()
		obj := map[string]interface{}{}
		obj["errors"] = org.Errors
		provide.Render(obj, 422, c)
	}
}

func updateOrganizationHandler(c *gin.Context) {
	provide.RenderError("not implemented", 501, c)
}

func deleteOrganizationHandler(c *gin.Context) {
	provide.RenderError("not implemented", 501, c)
}

func organizationInvitationsListHandler(c *gin.Context) {
	bearer := token.InContext(c)
	userID := bearer.UserID
	applicationID := bearer.ApplicationID

	if (userID == nil || *userID == uuid.Nil) && (applicationID == nil || *applicationID == uuid.Nil) {
		provide.RenderError("unauthorized", 401, c)
		return
	}

	organizationID, err := uuid.FromString(c.Param("id"))
	if err != nil {
		provide.RenderError(err.Error(), 422, c)
		return
	}

	org := &Organization{}
	query := dbconf.DatabaseConnection()
	resolveOrganization(query, &organizationID, applicationID, userID).Find(&org)

	if org == nil || org.ID == uuid.Nil {
		provide.RenderError("unauthorized", 401, c)
		return
	}

	invitations := org.pendingInvitations() // FIXME-- paginate the in-memory invitations list within redis
	invitedUsers := make([]*user.User, 0)
	for _, invite := range invitations {
		usr := &user.User{
			Name:  invite.Name,
			Email: invite.Email,
		}
		if invite.Permissions != nil {
			usr.Permissions = *invite.Permissions
		}
		invitedUsers = append(invitedUsers, usr)
	}
	provide.Render(invitedUsers, 200, c)
}

func organizationUsersListHandler(c *gin.Context) {
	bearer := token.InContext(c)
	userID := bearer.UserID
	applicationID := bearer.ApplicationID

	if (userID == nil || *userID == uuid.Nil) && (applicationID == nil || *applicationID == uuid.Nil) {
		provide.RenderError("unauthorized", 401, c)
		return
	}

	organizationID, err := uuid.FromString(c.Param("id"))
	if err != nil {
		provide.RenderError(err.Error(), 422, c)
		return
	}

	org := &Organization{}
	query := dbconf.DatabaseConnection()
	resolveOrganization(query, &organizationID, applicationID, userID).Find(&org)

	if org == nil || org.ID == uuid.Nil {
		provide.RenderError("unauthorized", 401, c)
		return
	}

	usersQuery := resolveOrganizationUsers(query, organizationID, applicationID).Order("users.created_at ASC")

	var users []*user.User
	provide.Paginate(c, usersQuery, &user.User{}).Find(&users)
	provide.Render(users, 200, c)
}

func createOrganizationUserHandler(c *gin.Context) {
	bearer := token.InContext(c)
	userID := bearer.UserID

	if userID == nil || *userID == uuid.Nil {
		provide.RenderError("unauthorized", 401, c)
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

	organizationID, err := uuid.FromString(c.Param("id"))
	if err != nil {
		provide.RenderError(err.Error(), 422, c)
		return
	}

	if userID == nil {
		if userIDStr, userIDStrOk := params["user_id"].(string); userIDStrOk {
			usrID, err := uuid.FromString(userIDStr)
			if err != nil {
				provide.RenderError(err.Error(), 422, c)
				return
			}
			userID = &usrID
		}
	}

	var invite *user.Invite
	var permissions common.Permission

	if invitationToken, invitationTokenOk := params["invitation_token"].(string); invitationTokenOk {
		invite, err = user.ParseInvite(invitationToken, false)
		if err != nil {
			provide.RenderError(err.Error(), 422, c)
			return
		}

		if invite.OrganizationID == nil {
			provide.RenderError("invitation did not specify organization_id", 403, c)
			return
		}

		if invite.OrganizationID.String() != organizationID.String() {
			provide.RenderError("invitation organization_id did not match authorized organization", 403, c)
			return
		}

		if invite.UserID != nil && userID != nil && invite.UserID.String() != userID.String() {
			provide.RenderError("invitation user_id did not match authorized user", 403, c)
			return
		}
	}

	orgPermissions, permissionsOk := params["permissions"].(common.Permission)
	if permissionsOk && !bearer.HasAnyExtendedPermission(organizationResourceKey, common.CreateResource, common.GrantResourceAuthorization) {
		provide.RenderError("unable to assert arbitrary organization user permissions", 403, c)
		return
	} else if permissionsOk {
		permissions = orgPermissions
	} else if invite.Permissions != nil {
		permissions = *invite.Permissions
	} else {
		permissions = common.DefaultApplicationOrganizationPermission
	}

	db := dbconf.DatabaseConnection()
	tx := db.Begin()

	org := &Organization{}
	resolveOrganization(db, &organizationID, nil, nil).Find(&org)
	if org == nil || org.ID == uuid.Nil {
		provide.RenderError("organization not found", 404, c)
		return
	}

	usr := &user.User{}
	db.Where("id = ?", userID).Find(&usr)
	if usr == nil || usr.ID == uuid.Nil {
		provide.RenderError("user not found", 404, c)
		return
	}

	success := org.addUser(tx, *usr, permissions)
	if success && invite != nil {
		success = invite.Token.IsRevoked() || invite.Token.Revoke(tx)
		if success {
			invite.InvalidateCache()
		}
	}

	if success {
		tx.Commit()
		provide.Render(nil, 204, c)
	} else {
		tx.Rollback()
		obj := map[string]interface{}{}
		obj["errors"] = org.Errors
		provide.Render(obj, 422, c)
	}
}

func updateOrganizationUserHandler(c *gin.Context) {
	provide.RenderError("not implemented", 501, c)
}

func deleteOrganizationUserHandler(c *gin.Context) {
	bearer := token.InContext(c)
	userID := bearer.UserID

	if userID == nil || *userID == uuid.Nil {
		provide.RenderError("unauthorized", 401, c)
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

	organizationID, err := uuid.FromString(c.Param("id"))
	if err != nil {
		provide.RenderError(err.Error(), 422, c)
		return
	}

	if userID == nil {
		if userIDStr, userIDStrOk := params["user_id"].(string); userIDStrOk {
			usrID, err := uuid.FromString(userIDStr)
			if err != nil {
				provide.RenderError(err.Error(), 422, c)
				return
			}
			userID = &usrID
		}
	}

	db := dbconf.DatabaseConnection()

	org := &Organization{}
	resolveOrganization(dbconf.DatabaseConnection(), &organizationID, nil, nil).Find(&org)
	if org == nil || org.ID == uuid.Nil {
		provide.RenderError("organization not found", 404, c)
		return
	}

	usr := &user.User{}
	db.Where("id = ?", userID).Find(&usr)
	if usr == nil || usr.ID == uuid.Nil {
		provide.RenderError("user not found", 404, c)
		return
	}

	if org.removeUser(db, usr) {
		provide.Render(nil, 204, c)
	} else {
		obj := map[string]interface{}{}
		obj["errors"] = org.Errors
		provide.Render(obj, 422, c)
	}
}
