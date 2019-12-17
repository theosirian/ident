package common

// Permission is a bitmask representing authorized privileges for user
// or authorization token; the permissions namespace has been split into
// partitions for readability: 2^24 permissions
type Permission uint32

const (
	// Authenticate permission
	Authenticate Permission = 0x1

	// ListResources generic permission
	ListResources Permission = 0x2

	// CreateResource generic permission
	CreateResource Permission = 0x4

	// UpdateResource generic permission
	UpdateResource Permission = 0x8

	// DeleteResource generic permission
	DeleteResource Permission = 0x10

	// GrantResourceAuthorization generic permission
	GrantResourceAuthorization Permission = 0x20

	// RevokeResourceAuthorization generic permission
	RevokeResourceAuthorization Permission = 0x40

	// Ident-specific permissions begin at 2^7

	// ListApplications permission
	ListApplications Permission = 0x80

	// CreateApplication permission
	CreateApplication Permission = 0x100

	// UpdateApplication permission
	UpdateApplication Permission = 0x200

	// DeleteApplication permission
	DeleteApplication Permission = 0x400

	// ListApplicationTokens permission
	// ListApplicationTokens Permission = 0x800

	// CreateApplicationToken permission
	// CreateApplicationToken Permission = 0x1000

	// DeleteApplicationToken permission
	// DeleteApplicationToken Permission = 0x2000

	// Privileged permissions begin at 2^20

	// ListUsers permission for administrative listing of users
	ListUsers Permission = 0x100000

	// CreateUser permission for administrative creation of new users
	CreateUser Permission = 0x200000

	// UpdateUser permission for administrative updates to existing users
	UpdateUser Permission = 0x400000

	// DeleteUser permission for administratively removing users
	DeleteUser Permission = 0x800000

	// ListTokens permission for administration to retrieve a list of all legacy auth tokens
	ListTokens Permission = 0x1000000

	// CreateToken permission for administratively creating new legacy auth tokens
	CreateToken Permission = 0x2000000

	// DeleteToken permission for administratively revoking legacy auth tokens
	DeleteToken Permission = 0x4000000

	// Sudo permission
	Sudo Permission = 0x20000000
)

// DefaultUserPermission is the default mask to use if permissions are not explicitly set upon user creation
const DefaultUserPermission Permission = Authenticate | ListApplications | CreateApplication | UpdateApplication

// DefaultAuth0RequestPermission is the ephemeral permission mask to apply to Auth0 requests
const DefaultAuth0RequestPermission = ListUsers | CreateUser

// DefaultSudoerPermission is the default mask to use when a new sudoer is created
const DefaultSudoerPermission = DefaultUserPermission | Sudo

// set updates the mask with the given permissions; package private
func (p Permission) set(flags Permission) Permission {
	return p | flags
}

// Has checks for the presence of the given permissions
func (p Permission) Has(flags Permission) bool {
	return p&flags != 0
}
